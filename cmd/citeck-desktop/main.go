package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
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

	// Context for daemon lifecycle — canceled when Wails quits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Reverse proxy to daemon via Unix socket — avoids TCP port exposure.
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "localhost"
		},
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				msg := "daemon starting"
				if lastErr := daemonStatus.LastError(); lastErr != "" {
					msg = lastErr
				}
				http.Error(w, msg, http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorPageHTML(daemonStatus, proxyErr)))
		},
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

	// Loading/error page handler — served until daemon is ready, then proxies
	loadingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-daemonReady:
			proxy.ServeHTTP(w, r)
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
		Description: "Citeck ECOS Platform Launcher",
		Icon:        citeckLogo,
		Assets: application.AssetOptions{
			Handler: loadingHandler,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			slog.Info("Wails shutting down, stopping daemon")
			cancel()
		},
	})

	// Main window — loads from Wails AssetServer (which proxies to daemon)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:       "main",
		Title:      "Citeck Launcher",
		DevToolsEnabled: true,
		Width:  1280,
		Height: 800,
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: false,
		},
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
	menu.Add("System Dump").OnClick(func(_ *application.Context) {
		dumpSystemInfo(socketPath)
	})
	menu.Add("Open Launcher Dir").OnClick(func(_ *application.Context) {
		_ = desktop.OpenBrowser("file://" + config.HomeDir())
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
// Shows daemon startup progress, last error, and retry count.
func errorPageHTML(status *desktop.DaemonStatus, proxyErr error) string {
	title := "Starting..."
	errMsg := ""
	errClass := ""

	if status != nil {
		if lastErr := status.LastError(); lastErr != "" {
			title = "Daemon restarting..."
			errMsg = lastErr
			errClass = "error"
			if f := status.Failures(); f > 1 {
				title = fmt.Sprintf("Daemon restarting (attempt %d)...", f+1)
			}
		}
	}
	if proxyErr != nil && errMsg == "" {
		errMsg = proxyErr.Error()
		errClass = "warn"
		title = "Connecting to daemon..."
	}

	errorBlock := ""
	if errMsg != "" {
		errorBlock = fmt.Sprintf(`<div class="%s">%s</div>`, errClass, errMsg)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta http-equiv="refresh" content="2"><style>
body{margin:0;height:100vh;display:flex;align-items:center;justify-content:center;
background:#1e1e1e;color:#888;font-family:system-ui,sans-serif;font-size:14px}
.loader{text-align:center;max-width:600px;padding:20px}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%%;animation:spin 1s linear infinite;margin:0 auto 12px}
@keyframes spin{to{transform:rotate(360deg)}}
.error{margin-top:16px;padding:12px;background:#2a1a1a;border:1px solid #5c2020;
border-radius:6px;color:#ef5350;font-size:12px;text-align:left;word-break:break-word;
font-family:monospace;max-height:200px;overflow:auto}
.warn{margin-top:16px;padding:12px;background:#2a2a1a;border:1px solid #5c5c20;
border-radius:6px;color:#ffa726;font-size:12px;text-align:left;word-break:break-word;
font-family:monospace}
</style></head><body><div class="loader"><div class="spinner"></div>%s%s</div></body></html>`,
		title, errorBlock)
}
