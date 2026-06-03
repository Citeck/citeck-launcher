//go:build desktop

// The desktop command pulls in Wails + wailswin (CGO/GTK), which only build
// under the desktop/gtk3 tags. Without this constraint `go vet ./...` and
// `go build ./...` (no tags) fail trying to compile it against the
// build-constraint-excluded wailswin package. `make build-desktop` passes
// `-tags desktop,gtk3`, so the real desktop build still includes these files.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/cli"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/citeck/citeck-launcher/internal/desktop/wailswin"
	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed logo.png
var citeckLogo []byte

// Build metadata injected via ldflags (see Makefile build-desktop). gitCommit /
// buildDate are forwarded to the daemon child's BuildInfo so the supervised
// daemon reports the same version as the wrapper.
var (
	version   = "dev"
	gitCommit = ""
	buildDate = ""
)

func main() {
	// Thin-wrapper dispatch: the supervisor spawns this same binary
	// (SelectDaemonBinary returns os.Executable()) with `start --_daemon
	// --desktop` to run the DAEMON, not a second GUI. Detect that invocation and
	// route it through the CLI (cobra → runDaemonMode → daemon.Start, blocking),
	// exactly like the server binary. Without this the spawned child would
	// re-launch the Wails GUI, hit the single-instance lock, and crash-loop.
	// cli.Execute calls os.Exit, so this never returns for the daemon child.
	if isDaemonInvocation() {
		cli.Execute(cli.BuildInfo{Version: version, Commit: gitCommit, BuildDate: buildDate})
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// isDaemonInvocation reports whether this process was started as the daemon
// child (`... start --_daemon ...`). The flag is the same hidden one the server
// binary uses; matching it as a bare token is sufficient because the supervisor
// passes it as a standalone argument.
func isDaemonInvocation() bool {
	return slices.Contains(os.Args[1:], "--_daemon")
}

// run wires the wrapper and blocks in app.Run until the GUI exits. It returns an
// error for any fatal startup failure; main turns that into log.Fatal. Keeping
// the body in run (rather than main) lets the deferred cleanups (lock release,
// ctx cancel) fire on an early error return instead of being skipped by a
// log.Fatal mid-startup (gocritic exitAfterDefer).
func run() error {
	// Set desktop mode early so config paths are correct
	config.SetDesktopMode(true)

	// Single instance check
	lock, err := desktop.AcquireInstanceLock()
	if err != nil {
		return err //nolint:wrapcheck // top-level startup error surfaced verbatim
	}
	defer lock.Release()

	// Context for daemon lifecycle — canceled when Wails quits OR the
	// process receives SIGINT/SIGTERM. The signal path is the only way the
	// daemon hits a graceful shutdown (and therefore SQLiteStore.Close →
	// WAL checkpoint) when the user runs `make run` and hits Ctrl-C, or
	// when systemd / a shell session sends TERM. Without it, the daemon's
	// background goroutine would just be killed mid-write and the WAL
	// could end up with the wrong "last word" on disk.
	//
	// The signal handler is installed below, after the Wails app is created,
	// so it can call app.Quit() (Wails' clean shutdown path: tears down
	// windows + the tray + the event loop, then fires OnShutdown which
	// cancels ctx). Canceling ctx without quitting Wails would gracefully
	// stop the daemon but leave the main Wails loop running, hanging the
	// terminal on Ctrl-C.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := config.SocketPath()

	// Observable daemon status — shared between the supervisor (LogWriter +
	// OnExit) and the proxy/loading handlers (LastError + LogLines for the
	// splash). The supervisor is created below, after the Wails app/window and
	// the control server exist, because its verb handlers touch the UI thread.
	daemonStatus := &desktop.DaemonStatus{}

	// HTTP client that connects to daemon via Unix socket.
	socketClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// daemonReady is closed once the supervised daemon child reports ready (or
	// the 30s budget elapses — we proxy anyway, mirroring the historical
	// behavior). The goroutine that polls sv.Ready() is started after the
	// supervisor is created (below); the gate is consumed by the asset handler.
	daemonReady := make(chan struct{})

	// windowManager is wired below once Wails is constructed. The asset handler
	// reads it via *windowManager (Wails calls the handler before app.Run, after
	// windowManager is assigned), so /desktop/windows/* routes into it.
	var windowManager *wailswin.WindowManager

	// In-flight guard shared by the tray "Dump System Info" item and the web
	// UI's /desktop/system-dump route, so a tray dump and a button dump can't
	// run two parallel exports at once (matches Kotlin LoadingDialog modal
	// semantics — a second trigger is a no-op while the first is running).
	var dumpInFlight atomic.Bool

	loadingHandler := newAssetHandler(assetDeps{
		socketPath:   socketPath,
		socketClient: socketClient,
		daemonStatus: daemonStatus,
		daemonReady:  daemonReady,
		windowMgr:    &windowManager,
		dumpInFlight: &dumpInFlight,
	})

	app := application.New(application.Options{
		Name:        "Citeck Launcher",
		Description: "Citeck Platform Launcher",
		Icon:        citeckLogo,
		Assets: application.AssetOptions{
			Handler: loadingHandler,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			slog.Info("Wails shutting down, stopping daemon")
			// Order: stop the child daemon (graceful — drains the SQLite WAL),
			// then close the control server (releases the wrapper socket), then
			// tear down native windows, then cancel ctx. supervisor/controlServer
			// are wired below before app.Run, so they are non-nil here.
			if supervisor != nil {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = supervisor.Stop(stopCtx)
				stopCancel()
			}
			if controlServer != nil {
				controlServer.Close()
			}
			if windowManager != nil {
				windowManager.Quit()
			}
			cancel()
		},
	})

	windowManager = wailswin.NewWindowManager(app)

	// SIGINT/SIGTERM → app.Quit() (Wails' own clean shutdown). Routing
	// through Wails — rather than canceling ctx directly — is necessary
	// because cancel alone stops the daemon but leaves the Wails event
	// loop running, hanging the terminal on Ctrl-C until the user kills
	// the process. app.Quit closes windows, fires OnShutdown (which in
	// turn stops the daemon child, closes the control server, calls cancel,
	// and drains the daemon), and exits Run().
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("Signal received, requesting graceful shutdown")
		app.Quit()
		// Second-signal escape: if Wails / the daemon shutdown is hung
		// (e.g. Docker stuck stopping containers), let the user force-exit
		// with another ^C instead of being trapped in the terminal.
		go func() {
			<-sigCh
			slog.Warn("Second signal received, forcing exit")
			os.Exit(1)
		}()
	}()

	// Strip a "dev-" prefix from link-time-injected version so the title reads
	// "Citeck Launcher v20260527-..." instead of "Citeck Launcher vdev-..."
	titleVersion := strings.TrimPrefix(version, "dev-")
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "main",
		Title:           fmt.Sprintf("Citeck Launcher v%s", titleVersion),
		DevToolsEnabled: true,
		Width:           1200,
		Height:          800,
		MinWidth:        300,
		MinHeight:       400,
		// F12 mirrors the tray "DevTools" entry — common browser muscle memory.
		KeyBindings: map[string]func(application.Window){
			"F12": func(w application.Window) { w.OpenDevTools() },
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: false,
		},
	})

	// raiseToFront shows the window and brings it above other windows with
	// focus. On Linux/GTK, Focus() maps to gtk_window_present, which is subject
	// to the window manager's focus-stealing prevention and intermittently
	// leaves the window behind others when shown from the tray. Forcing
	// keep-above raises it reliably; we drop the hint on the next UI-loop tick
	// so normal stacking resumes (the window stays raised+focused, just no
	// longer pinned above everything). Must be called on the UI thread.
	raiseToFront := func() {
		window.Show()
		if runtime.GOOS == "linux" {
			// Linux/GTK: Show() maps the window asynchronously, and Focus()
			// (gtk_window_present) gets denied by the WM's focus-stealing
			// prevention on a tray click — it blinks the taskbar instead of
			// raising. Pin keep-above so the WM restacks the window above
			// others when it maps, regardless of focus rules, then release the
			// pin after a delay long enough for the map+restack to land.
			// Dropping it on the next UI tick raced the async GTK map and left
			// the window behind (the bug this replaces).
			window.SetAlwaysOnTop(true)
			window.Focus()
			time.AfterFunc(700*time.Millisecond, func() {
				application.InvokeAsync(func() { window.SetAlwaysOnTop(false) })
			})
		} else {
			window.Focus()
		}
	}

	// dispatchVerb performs a native verb on the UI thread. It is shared by the
	// control-server handlers (which the daemon POSTs to over the wrapper
	// socket) and the data-driven tray menu OnClicks, so both paths stay in
	// sync. Returns an error for an unknown/invalid verb so the control server
	// can surface it; UI actions are queued via InvokeAsync (calling Wails
	// window/app methods directly off the UI thread deadlocks GTK).
	dispatchVerb := func(verb string, params map[string]any) error {
		switch verb {
		case desktop.VerbWindowFocus:
			application.InvokeAsync(raiseToFront)
		case desktop.VerbWindowShow:
			application.InvokeAsync(func() { window.Show() })
		case desktop.VerbWindowHide:
			application.InvokeAsync(func() { window.Hide() })
		case desktop.VerbDevtoolsOpen:
			application.InvokeAsync(func() { window.OpenDevTools() })
		case desktop.VerbAppQuit:
			application.InvokeAsync(func() { app.Quit() })
		case desktop.VerbShellOpenPath:
			path, _ := params["path"].(string)
			if path == "" {
				return fmt.Errorf("%s: empty path", desktop.VerbShellOpenPath)
			}
			return desktop.OpenBrowser("file://" + path)
		case desktop.VerbShellOpenURL:
			url, _ := params["url"].(string)
			if url == "" {
				return fmt.Errorf("%s: empty url", desktop.VerbShellOpenURL)
			}
			return desktop.OpenBrowser(url)
		default:
			return fmt.Errorf("unsupported verb: %s", verb)
		}
		return nil
	}

	// Control server: the daemon (a separate process) calls native verbs over
	// the wrapper unix socket. Register handlers BEFORE building capabilities so
	// the advertised verb set honestly reflects what is wired. Only register
	// verbs whose Wails API is confirmed in this alpha — anything else stays out
	// of cs.Verbs() and is excluded from the advertised capabilities.
	controlServer = desktop.NewControlServer(config.WrapperSocketPath())
	controlServer.Handle(desktop.VerbWindowFocus, func(json.RawMessage) (any, error) {
		return nil, dispatchVerb(desktop.VerbWindowFocus, nil)
	})
	controlServer.Handle(desktop.VerbAppQuit, func(json.RawMessage) (any, error) {
		return nil, dispatchVerb(desktop.VerbAppQuit, nil)
	})
	controlServer.Handle(desktop.VerbDevtoolsOpen, func(json.RawMessage) (any, error) {
		return nil, dispatchVerb(desktop.VerbDevtoolsOpen, nil)
	})
	controlServer.Handle(desktop.VerbWindowShow, func(json.RawMessage) (any, error) {
		return nil, dispatchVerb(desktop.VerbWindowShow, nil)
	})
	controlServer.Handle(desktop.VerbWindowHide, func(json.RawMessage) (any, error) {
		return nil, dispatchVerb(desktop.VerbWindowHide, nil)
	})
	controlServer.Handle(desktop.VerbShellOpenPath, func(p json.RawMessage) (any, error) {
		var args struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(p, &args)
		return nil, dispatchVerb(desktop.VerbShellOpenPath, map[string]any{"path": args.Path})
	})
	controlServer.Handle(desktop.VerbShellOpenURL, func(p json.RawMessage) (any, error) {
		var args struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(p, &args)
		return nil, dispatchVerb(desktop.VerbShellOpenURL, map[string]any{"url": args.URL})
	})
	controlServer.Handle(desktop.VerbUpdateApply, func(p json.RawMessage) (any, error) {
		var args struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(p, &args)
		// Defense-in-depth: the control socket is local, but reject any version
		// that is not clean semver before it reaches manifest/state operations,
		// consistent with the daemon-side guard in update.Service.Stage.
		if !update.IsValidVersion(args.Version) {
			return nil, fmt.Errorf("%s: invalid version %q", desktop.VerbUpdateApply, args.Version)
		}
		// Async: the daemon that called this is about to be replaced. Returning
		// immediately lets it flush its HTTP response before we stop it.
		go applyDaemonSwap(ctx, args.Version, window)
		return nil, nil
	})
	if startErr := controlServer.Start(); startErr != nil {
		return fmt.Errorf("start wrapper control server: %w", startErr)
	}

	// Build capabilities AFTER registering handlers so Verbs() reflects exactly
	// what we wired, then start the supervisor with the wrapper socket + caps in
	// the child's environment.
	caps := desktop.Capabilities{ContractVersion: desktop.CapsContractVersion, Verbs: controlServer.Verbs()}

	supervisor = &desktop.Supervisor{
		// Re-resolve on every spawn so a staged auto-update payload / rollback
		// takes effect on restart (Spec 2b). currentVersion is our own ldflags
		// version; SelectDaemonBinary never downgrades below it.
		BinarySelector: func() (string, error) { return desktop.SelectDaemonBinary(version) },
		// Empty master-password line; the desktop daemon ignores opts.MasterPassword
		// (default-password auto-unlocks; custom password defers to the Web UI),
		// so feeding an empty line preserves behavior. See supervisor.go.
		Stdin: "\n",
		ExtraEnv: []string{
			"CITECK_WRAPPER_SOCK=" + config.WrapperSocketPath(),
			"CITECK_WRAPPER_CAPS=" + caps.Encode(),
		},
		LogWriter: daemonStatus.LogWriter(),
		// Restore the in-process loop's splash-error parity: surface the daemon's
		// last exit error on the loading/error page.
		OnExit: daemonStatus.SetError,
	}
	if startErr := supervisor.Start(ctx); startErr != nil {
		return fmt.Errorf("start daemon supervisor: %w", startErr)
	}

	// Wait for the daemon to become ready, then open the readiness gate. Mirror
	// the historical behavior: after 30s, proxy anyway (do NOT hard-fail) so a
	// slow-but-eventually-ready daemon still gets through.
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			if supervisor.Ready() {
				slog.Info("Daemon ready", "socket", socketPath)
				break
			}
			if time.Now().After(deadline) {
				slog.Warn("Daemon not ready after 30s, proxying anyway")
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		close(daemonReady)
	}()

	// Second-launch focus hand-off (Kotlin AppLocalSocket parity) now flows
	// entirely through the control server: a new wrapper → NotifyExistingInstance
	// → the running daemon's POST /desktop/focus → daemon calls the wrapper
	// control socket's "window.focus" verb → dispatchVerb(raiseToFront). No
	// process-global daemon callback is needed anymore.

	// Hide on close instead of quitting (minimize to tray)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// System tray. We build a minimal hardcoded fallback menu immediately so the
	// tray is usable before the daemon is ready, then replace it with the
	// backend-defined (data-driven) menu once the daemon answers.
	tray := app.SystemTray.New()
	tray.SetLabel("Citeck Launcher")
	tray.SetTooltip("Citeck Launcher")
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(citeckLogo)
	} else {
		tray.SetIcon(citeckLogo)
	}

	fallback := app.NewMenu()
	fallback.Add("Open").OnClick(func(_ *application.Context) { raiseToFront() })
	fallback.AddSeparator()
	fallback.Add("Exit").OnClick(func(_ *application.Context) { app.Quit() })
	tray.SetMenu(fallback)

	// Left-click on the tray icon raises the window to the front — including
	// when it's already open but behind other windows (Show() is then a no-op
	// and the keep-above raise in raiseToFront does the work).
	tray.OnClick(raiseToFront)

	// Once the daemon is ready, fetch its tray menu and replace the fallback
	// with the data-driven native menu. The dump item reuses the existing
	// in-flight-guarded dump flow; verb items dispatch via dispatchVerb.
	go func() {
		<-daemonReady
		menu, err := buildTrayMenuFromBackend(app, socketClient, dispatchVerb, &dumpInFlight, socketPath)
		if err != nil {
			slog.Warn("Failed to build tray menu from backend; keeping fallback", "err", err)
			return
		}
		// Wails alpha.95 live-replaces the menu via SystemTray.SetMenu
		// (linuxSystemTray.setMenu rebuilds the D-Bus menu + refresh), so no
		// tray recreation is needed.
		application.InvokeAsync(func() { tray.SetMenu(menu) })
	}()

	// app.Run blocks until the GUI exits. As before, a run error is logged (not
	// returned) so the process still exits 0 on a normal Wails teardown; the
	// deferred lock release + ctx cancel fire on return.
	if runErr := app.Run(); runErr != nil {
		slog.Error("Application exited with error", "err", runErr)
	}
	return nil
}

// applySwapSettleDelay gives the just-responded daemon a moment to flush its
// HTTP response to the webview before we stop it for the swap.
const applySwapSettleDelay = 300 * time.Millisecond

// applyDaemonSwap performs the health-gated daemon swap on the wrapper side
// (Spec 2b). The staged (pending) payload is already chosen by SelectDaemonBinary
// (it is newer than our bundled version). On health-gate failure it marks the
// payload failed — so SelectDaemonBinary then returns the previous good / bundled
// binary — and restarts into that (rollback). Either way it reloads the webview so
// the UI reflects the now-running daemon and its /desktop/update/status.
func applyDaemonSwap(ctx context.Context, version string, window *application.WebviewWindow) {
	time.Sleep(applySwapSettleDelay)
	updatesDir := config.UpdatesDir()

	if err := supervisor.Restart(ctx, desktop.UpdateHealthTimeout); err != nil {
		slog.Error("Daemon update failed health-gate; rolling back", "version", version, "err", err)
		if merr := update.MarkState(updatesDir, version, update.StateFailed); merr != nil {
			slog.Error("Failed to mark update failed", "err", merr)
		}
		if rerr := supervisor.Restart(ctx, desktop.UpdateHealthTimeout); rerr != nil {
			slog.Error("Rollback restart also failed", "err", rerr)
		}
	} else {
		if merr := update.MarkState(updatesDir, version, update.StateGood); merr != nil {
			slog.Error("Failed to mark update good", "err", merr)
		}
		slog.Info("Daemon update applied", "version", version)
	}
	window.Reload() // re-request assets through the proxy → the now-running daemon
}

// supervisor and controlServer are package-level so OnShutdown (an
// application.Options field set before they are constructed) can reference them.
// They are assigned in run before app.Run, and only read in the OnShutdown
// callback, which Wails fires on the UI thread during shutdown — after run has
// finished wiring them — so no synchronization is required.
var (
	supervisor    *desktop.Supervisor
	controlServer *desktop.ControlServer
)

// buildTrayMenuFromBackend fetches GET api.DesktopTrayMenu over the daemon
// socket and builds a native Wails menu from it. Verb items dispatch through
// dispatchVerb; the backend "system-dump" item reuses the existing in-flight-
// guarded dump flow (label toggle + dialogs), preserving Kotlin LoadingDialog
// parity. Unknown action kinds/endpoints are skipped.
func buildTrayMenuFromBackend(
	app *application.App,
	socketClient *http.Client,
	dispatchVerb func(verb string, params map[string]any) error,
	dumpInFlight *atomic.Bool,
	socketPath string,
) (*application.Menu, error) {
	req, err := http.NewRequest(http.MethodGet, "http://daemon"+api.DesktopTrayMenu, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build tray-menu request: %w", err)
	}
	resp, err := socketClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tray menu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tray-menu endpoint returned %d", resp.StatusCode)
	}
	var tm daemon.TrayMenu
	if err := json.NewDecoder(resp.Body).Decode(&tm); err != nil {
		return nil, fmt.Errorf("decode tray menu: %w", err)
	}

	menu := app.NewMenu()
	for _, item := range tm.Items {
		switch item.Action.Kind {
		case "verb":
			verb := item.Action.Verb
			params := item.Action.Params
			mi := menu.Add(item.Label)
			mi.SetEnabled(item.Enabled)
			mi.OnClick(func(_ *application.Context) {
				if derr := dispatchVerb(verb, params); derr != nil {
					slog.Warn("Tray verb failed", "verb", verb, "err", derr)
				}
			})
		case "backend":
			if item.Action.Endpoint == "/desktop/system-dump" {
				wireDumpItem(menu.Add(item.Label), app, dumpInFlight, socketPath)
				continue
			}
			slog.Warn("Skipping tray item with unsupported backend endpoint", "endpoint", item.Action.Endpoint)
		default:
			slog.Warn("Skipping tray item with unknown action kind", "kind", item.Action.Kind)
		}
	}
	return menu, nil
}

// wireDumpItem attaches the native system-dump flow to a tray menu item. It
// reuses the shared dumpInFlight guard (so a tray dump and a web-UI dump can't
// run in parallel), toggles the item's label/enabled while running, opens the
// containing folder, and shows a success/error dialog — identical to the legacy
// hardcoded dumpItem.
func wireDumpItem(dumpItem *application.MenuItem, app *application.App, dumpInFlight *atomic.Bool, socketPath string) {
	const idle = "Dump System Info"
	dumpItem.OnClick(func(_ *application.Context) {
		if !dumpInFlight.CompareAndSwap(false, true) {
			return
		}
		dumpItem.SetEnabled(false)
		dumpItem.SetLabel(idle + " (running...)")
		go func() {
			defer func() {
				application.InvokeAsync(func() {
					dumpItem.SetLabel(idle)
					dumpItem.SetEnabled(true)
				})
				dumpInFlight.Store(false)
			}()
			zipPath, err := dumpSystemInfo(socketPath)
			application.InvokeAsync(func() {
				if err != nil {
					slog.Error("System dump failed", "err", err)
					app.Dialog.Error().
						SetTitle("System Dump Failed").
						SetMessage(err.Error()).
						Show()
					return
				}
				slog.Info("System dump created", "path", zipPath)
				if openErr := desktop.OpenBrowser("file://" + filepath.Dir(zipPath)); openErr != nil {
					slog.Warn("Failed to open dump folder", "err", openErr)
				}
				app.Dialog.Info().
					SetTitle("System Dump Saved").
					SetMessage("System dump saved to:\n" + zipPath).
					Show()
			})
		}()
	})
}

// assetDeps bundles the shared state the Wails asset handler needs. windowMgr is
// a double pointer because the WindowManager is assigned after this handler is
// constructed (Wails dereferences it lazily, at request time, by which point it
// is wired).
type assetDeps struct {
	socketPath   string
	socketClient *http.Client
	daemonStatus *desktop.DaemonStatus
	daemonReady  <-chan struct{}
	windowMgr    **wailswin.WindowManager
	dumpInFlight *atomic.Bool
}

// newAssetHandler builds the loading/error + proxy asset handler. It is served
// until the daemon is ready, then proxies to the daemon over the unix socket.
// /desktop/windows/* and /desktop/system-dump are intercepted (Wails-only APIs).
func newAssetHandler(d assetDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/desktop/windows/") {
			wm := *d.windowMgr
			if wm == nil {
				http.Error(w, "window manager not ready", http.StatusServiceUnavailable)
				return
			}
			http.StripPrefix("/desktop/windows", wm.HTTPHandler()).ServeHTTP(w, r)
			return
		}
		// Native system dump for the web UI. The browser <a download> path the
		// web button used does nothing in the WebKitGTK webview (no download
		// handler), so the desktop UI routes here instead: write the ZIP to
		// disk and open the containing folder, exactly like the tray item and
		// Kotlin 1.x's SystemDumpUtils (Desktop.open(reportDir)). Returns the
		// saved path so the frontend can show it in the success toast.
		if r.URL.Path == "/desktop/system-dump" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleDesktopSystemDump(w, d.socketPath, d.dumpInFlight)
			return
		}
		select {
		case <-d.daemonReady:
			proxyViaSocket(w, r, d.socketClient, d.daemonStatus)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "daemon starting", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorPageHTML(d.daemonStatus, nil)))
		}
	}
}

// proxyViaSocket forwards the request to the daemon via the Unix socket
// manually. Wails AssetServer sends the body with ContentLength=0 (streamed),
// which breaks httputil.ReverseProxy; a direct HTTP client handles it correctly.
func proxyViaSocket(w http.ResponseWriter, r *http.Request, socketClient *http.Client, daemonStatus *desktop.DaemonStatus) {
	// Buffer body — Wails may stream it with ContentLength=0
	var bodyReader io.Reader = http.NoBody
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}
	}

	targetURL := "http://localhost" + r.URL.RequestURI()
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader) //nolint:gosec // G704: proxy to local Unix socket, not user-controlled URL
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Copy headers
	for k, vv := range r.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}

	resp, err := socketClient.Do(proxyReq) //nolint:gosec // G704: proxy to local Unix socket, not user-controlled URL
	if err != nil {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			msg := "daemon starting"
			if lastErr := daemonStatus.LastError(); lastErr != "" {
				msg = lastErr
			}
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(errorPageHTML(daemonStatus, err)))
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Stream the (possibly SSE / chunked-log) response body to the webview.
	streamResponseBody(w, resp.Body)
}

// handleDesktopSystemDump runs the native system dump on behalf of the web UI
// button. The browser <a download> path the button used does nothing in the
// WebKitGTK webview (no download handler), so the desktop UI POSTs here: we
// write the ZIP to disk and open its folder, exactly like the tray item and
// Kotlin 1.x's SystemDumpUtils (Desktop.open(reportDir)), then return the saved
// path so the frontend can show it. dumpInFlight is shared with the tray item
// so the two can't export in parallel.
func handleDesktopSystemDump(w http.ResponseWriter, socketPath string, dumpInFlight *atomic.Bool) {
	if !dumpInFlight.CompareAndSwap(false, true) {
		http.Error(w, "dump already in progress", http.StatusConflict)
		return
	}
	defer dumpInFlight.Store(false)
	zipPath, err := dumpSystemInfo(socketPath)
	if err != nil {
		slog.Error("System dump failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("System dump created", "path", zipPath)
	if openErr := desktop.OpenBrowser("file://" + filepath.Dir(zipPath)); openErr != nil {
		slog.Warn("Failed to open dump folder", "err", openErr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": zipPath})
}

// streamResponseBody copies a (possibly streaming) upstream response body to w,
// flushing after each chunk so SSE events and chunked logs arrive live. It stops
// on a write error: a broken pipe means the webview closed the request (the user
// switched the log tail or closed the window), so we must stop reading and let
// the caller's deferred Body.Close tear the upstream daemon stream down —
// otherwise the proxy goroutine and the daemon-side follow stream leak for the
// life of the process.
func streamResponseBody(w http.ResponseWriter, body io.Reader) {
	f, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, body)
		return
	}
	buf := make([]byte, 4096)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			f.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// errorPageHTML generates an informative loading/error page with auto-refresh.
// On error: shows the error message and startup logs so the user (and developer) can see what happened.
// On startup: shows a spinner with "Starting...".
func errorPageHTML(status *desktop.DaemonStatus, proxyErr error) string {
	title := "Starting..."
	errMsg := ""
	logLines := ""

	if status != nil {
		if lastErr := status.LastError(); lastErr != "" {
			title = "Daemon failed to start"
			errMsg = lastErr
			if f := status.Failures(); f > 1 {
				title = fmt.Sprintf("Daemon failed to start (attempt %d)", f)
			}
			logLines = status.LogLines()
		}
	}
	if proxyErr != nil && errMsg == "" {
		errMsg = proxyErr.Error()
		title = "Connecting to daemon..."
	}

	errorBlock := ""
	if errMsg != "" {
		errorBlock = `<div class="error-box">` + errMsg + `</div>`
	}
	logBlock := ""
	if logLines != "" {
		logBlock = `<div class="log-title">Startup log:</div><pre class="log-box">` + logLines + `</pre>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta http-equiv="refresh" content="2"><style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#1e1e1e;color:#888;font-family:system-ui,sans-serif;font-size:14px}
.loader{text-align:center;max-width:800px;width:100%%;padding:24px}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%%;animation:spin 1s linear infinite;margin:0 auto 12px}
@keyframes spin{to{transform:rotate(360deg)}}
h2{margin:0 0 8px;color:#ccc;font-size:16px}
.error-box{margin-top:12px;padding:10px 14px;background:#2a1a1a;border:1px solid #5c2020;
border-radius:6px;color:#ef5350;font-size:13px;text-align:left;word-break:break-word;font-family:monospace}
.log-title{margin-top:16px;font-size:12px;color:#666;text-align:left}
.log-box{margin-top:4px;padding:10px 14px;background:#161616;border:1px solid #333;
border-radius:6px;color:#999;font-size:11px;text-align:left;white-space:pre-wrap;word-break:break-all;
font-family:monospace;max-height:400px;overflow:auto;line-height:1.5}
</style></head><body><div class="loader"><div class="spinner"></div><h2>%s</h2>%s%s</div></body></html>`,
		title, errorBlock, logBlock)
}
