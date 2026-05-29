package main

import (
	"bytes"
	"context"
	_ "embed"
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
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/citeck/citeck-launcher/internal/desktop/wailswin"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed logo.png
var citeckLogo []byte

var version = "dev"

func main() {
	// Set desktop mode early so config paths are correct
	config.SetDesktopMode(true)

	// Single instance check
	lock, err := desktop.AcquireInstanceLock()
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Release()

	// Context for daemon lifecycle — canceled when Wails quits OR the
	// process receives SIGINT/SIGTERM. The signal path is the only way the
	// daemon hits a graceful shutdown (and therefore SQLiteStore.Close →
	// WAL checkpoint) when the user runs `make run` and hits Ctrl-C, or
	// when systemd / a shell session sends TERM. Without it, the daemon's
	// background goroutine would just be killed mid-write and the WAL
	// could end up with the wrong "last word" on disk.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("Signal received, requesting graceful shutdown")
		// First, let the daemon drain via the same path Wails uses on
		// window-quit (cancel propagates into daemon's external-ctx hook
		// → d.shutdown(false) → store.Close → WAL checkpoint).
		cancel()
		// Second-signal escape: if shutdown is hung (e.g. Docker daemon
		// stuck stopping containers), let the user force-exit with
		// another ^C instead of being trapped in the terminal.
		go func() {
			<-sigCh
			slog.Warn("Second signal received, forcing exit")
			os.Exit(1)
		}()
	}()

	// ReadyCh receives notification when daemon HTTP server is ready
	readyCh := make(chan string, 1)

	socketPath := config.SocketPath()

	// Observable daemon status — shared between daemon loop and proxy handlers
	daemonStatus := &desktop.DaemonStatus{}

	// Start daemon in background
	go desktop.RunDaemonLoop(ctx, desktop.DaemonOpts{
		Version: version,
		ReadyCh: readyCh,
		Status:  daemonStatus,
	})

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

	// proxyViaSockets forwards the request to the daemon via Unix socket manually.
	// Wails AssetServer sends body with ContentLength=0 (streamed), which breaks
	// httputil.ReverseProxy. Direct HTTP client handles this correctly.
	proxyViaSocket := func(w http.ResponseWriter, r *http.Request) {
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
		// Stream response body (supports SSE and chunked logs)
		if f, ok := w.(http.Flusher); ok {
			buf := make([]byte, 4096)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					_, _ = w.Write(buf[:n])
					f.Flush()
				}
				if readErr != nil {
					break
				}
			}
		} else {
			_, _ = io.Copy(w, resp.Body)
		}
	}

	// Wait for daemon in a goroutine, set ready flag
	daemonReady := make(chan struct{})
	go func() {
		select {
		case <-readyCh:
			slog.Info("Daemon ready", "socket", socketPath)
		case <-time.After(30 * time.Second):
			slog.Warn("Daemon not ready after 30s, proxying anyway")
		case <-ctx.Done():
			return
		}
		close(daemonReady)
	}()

	// windowManager is wired below once Wails is constructed. Declared here so
	// the asset handler closure (which Wails calls before app.Run) can route
	// /desktop/windows/* into it.
	var windowManager *wailswin.WindowManager

	// Loading/error page handler — served until daemon is ready, then proxies.
	// /desktop/windows/* is intercepted before the daemon proxy because it is
	// a Wails-only API (server mode has no native windows to manage).
	loadingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/desktop/windows/") {
			if windowManager == nil {
				http.Error(w, "window manager not ready", http.StatusServiceUnavailable)
				return
			}
			http.StripPrefix("/desktop/windows", windowManager.HTTPHandler()).ServeHTTP(w, r)
			return
		}
		select {
		case <-daemonReady:
			proxyViaSocket(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "daemon starting", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorPageHTML(daemonStatus, nil)))
		}
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
			if windowManager != nil {
				windowManager.Quit()
			}
			cancel()
		},
	})

	windowManager = wailswin.NewWindowManager(app)

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

	// Second-launch focus hand-off (Kotlin AppLocalSocket parity): the daemon
	// runs in its own goroutine and never exposes the *Daemon back to us, so
	// we register the raise callback via a process-global atomic in the daemon
	// package. Must run on the UI thread (application.InvokeAsync) — calling
	// Show/Focus directly from the HTTP handler goroutine deadlocks GTK.
	daemon.SetDesktopFocusHandler(func() {
		application.InvokeAsync(func() {
			window.Show()
			window.Focus()
		})
	})

	// Hide on close instead of quitting (minimize to tray)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// System tray
	tray := app.SystemTray.New()
	tray.SetLabel("Citeck Launcher")
	tray.SetTooltip("Citeck Launcher")
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(citeckLogo)
	} else {
		tray.SetIcon(citeckLogo)
	}

	menu := app.NewMenu()
	menu.Add("Open").OnClick(func(_ *application.Context) {
		window.Show()
		window.Focus()
	})

	// In-flight guard prevents double-click from launching parallel dumps
	// (matches Kotlin LoadingDialog modal semantics — the second click is a
	// no-op while the first is still running).
	var dumpInFlight atomic.Bool
	dumpItem := menu.Add("Dump System Info")
	dumpItem.OnClick(func(_ *application.Context) {
		if !dumpInFlight.CompareAndSwap(false, true) {
			return
		}
		dumpItem.SetEnabled(false)
		dumpItem.SetLabel("Dump System Info (running...)")
		go func() {
			defer func() {
				application.InvokeAsync(func() {
					dumpItem.SetLabel("Dump System Info")
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
	menu.Add("Open Launcher Dir").OnClick(func(_ *application.Context) {
		_ = desktop.OpenBrowser("file://" + config.HomeDir())
	})
	menu.Add("DevTools").OnClick(func(_ *application.Context) {
		window.OpenDevTools()
	})
	menu.AddSeparator()
	menu.Add("Exit").OnClick(func(_ *application.Context) {
		app.Quit()
	})

	tray.SetMenu(menu)
	tray.OnClick(func() {
		window.Show()
		window.Focus()
	})

	if runErr := app.Run(); runErr != nil {
		slog.Error("Application exited with error", "err", runErr)
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
