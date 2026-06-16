//go:build desktop

// The desktop command pulls in Wails + wailswin (CGO/GTK), which only build
// under the desktop/gtk3 tags. Without this constraint `go vet ./...` and
// `go build ./...` (no tags) fail trying to compile it against the
// build-constraint-excluded wailswin package. `make build-desktop` passes
// `-tags desktop,gtk3`, so the real desktop build still includes these files.
//
// The wrapper is split across files in this package: main.go (entry + run()
// wiring), tray.go (backend-defined tray menu), assethandler.go (the loading /
// proxy asset handler), and swap.go (the auto-update daemon swap).
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/cli"
	"github.com/citeck/citeck-launcher/internal/config"
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
				windowManager.CloseAll()
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
		Zoom:            wailswin.UIZoom,
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

	// Re-apply the UI zoom once the webview is ready. On macOS the init path
	// skips options.Zoom (setMagnification is only wired at runtime), so without
	// this the main window would render at 1.0 there; harmless re-apply on
	// Linux/Windows where options.Zoom already took effect.
	window.RegisterHook(events.Common.WindowRuntimeReady, func(_ *application.WindowEvent) {
		window.SetZoom(wailswin.UIZoom)
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
		go applyDaemonSwap(ctx, args.Version, window, socketClient)
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
		// takes effect on restart. currentVersion is our own ldflags
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
				// Reflect the RUNNING daemon's version in the title — it may be
				// newer than this wrapper after a daemon-only auto-update.
				refreshWindowTitle(socketClient, window)
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

	// Hide on close instead of quitting (minimize to tray). Close any secondary
	// windows (logs / editor) first so they don't outlive the hidden main window
	// — Kotlin parity: Main.kt onCloseRequest → CiteckWindow.closeAll() + hide.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if windowManager != nil {
			windowManager.CloseAll()
		}
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

// supervisor and controlServer are package-level so OnShutdown (an
// application.Options field set before they are constructed) can reference them.
// They are assigned in run before app.Run, and only read in the OnShutdown
// callback, which Wails fires on the UI thread during shutdown — after run has
// finished wiring them — so no synchronization is required.
var (
	supervisor    *desktop.Supervisor
	controlServer *desktop.ControlServer
)
