package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

var version = "dev"

const splashHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="2">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    background: #1e1f22; color: #dfe1e5;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    height: 100vh; font-size: 14px;
  }
  .title { font-size: 22px; font-weight: 600; margin-bottom: 12px; }
  .status { color: #9da0a8; margin-bottom: 32px; }
  .spinner {
    width: 36px; height: 36px; border: 3px solid #43454a;
    border-top-color: #4d9cf6; border-radius: 50%;
    animation: spin 0.8s linear infinite; margin-bottom: 32px;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .links { display: flex; gap: 20px; margin-top: 16px; }
  .links a {
    color: #4d9cf6; text-decoration: none; font-size: 13px;
    padding: 6px 14px; border: 1px solid #43454a; border-radius: 4px;
  }
  .links a:hover { background: #2b2d30; }
</style>
</head>
<body>
  <div class="spinner"></div>
  <div class="title">Citeck Launcher</div>
  <div class="status">Starting daemon...</div>
  <div class="links">
    <a href="/api/v1/daemon/logs?lines=200" target="_blank">Show Logs</a>
    <a href="/api/v1/system/dump?format=zip">System Dump</a>
  </div>
</body>
</html>`

func main() {
	// Set desktop mode early so config paths are correct
	config.SetDesktopMode(true)

	// Single instance check
	lock, err := desktop.AcquireInstanceLock()
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Release()

	// Context for daemon lifecycle — cancelled when Wails quits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ReadyCh receives the daemon URL when HTTP server is ready
	readyCh := make(chan string, 1)

	// Start daemon in a background goroutine with restart loop.
	// NoUI=true disables the TCP listener — the webview proxies through the Unix socket
	// which gives full access (socketMux: all routes, no CSRF, no rate limiting).
	go desktop.RunDaemonLoop(ctx, desktop.DaemonOpts{
		Version: version,
		ReadyCh: readyCh,
		NoUI:    true,
	})

	// Reverse proxy to daemon via Unix socket.
	// This routes through socketMux (full access), not tcpMux (restricted).
	// FlushInterval -1 enables immediate flushing for SSE streams.
	socketPath := config.SocketPath()
	proxy := &httputil.ReverseProxy{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", socketPath, 5*time.Second)
			},
		},
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: "http", Host: "localhost"})
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Daemon not ready yet — return splash page for HTML requests, 503 for API
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"daemon starting","code":"DAEMON_STARTING"}`))
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(splashHTML))
		},
	}

	// Static assets are embedded in the binary via daemon.WebUIHandler().
	// API requests are proxied to the daemon via Unix socket.
	staticHandler := daemon.WebUIHandler()

	assetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			proxy.ServeHTTP(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})

	app := application.New(application.Options{
		Name:        "Citeck Launcher",
		Description: "Citeck ECOS Platform Launcher",
		Icon:        icons.ApplicationDarkMode256,
		Assets: application.AssetOptions{
			Handler: assetHandler,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			slog.Info("Wails shutting down, stopping daemon")
			cancel()
		},
	})

	// Create main window (hidden initially, shown after daemon is ready)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "Citeck Launcher",
		Width:  1280,
		Height: 800,
		Hidden: true,
		URL:    "/",
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
	tray.SetTooltip("Citeck Launcher")
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.ApplicationDarkMode256)
	}

	menu := app.NewMenu()
	menu.Add("Open").OnClick(func(ctx *application.Context) {
		window.Show()
		window.Focus()
	})
	menu.Add("System Dump").OnClick(func(ctx *application.Context) {
		dumpSystemInfo(socketPath)
	})
	menu.Add("Open Launcher Dir").OnClick(func(ctx *application.Context) {
		desktop.OpenBrowser("file://" + config.HomeDir())
	})
	menu.AddSeparator()
	menu.Add("Exit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})

	tray.SetMenu(menu)
	tray.OnClick(func() {
		window.Show()
		window.Focus()
	})

	// Show window once daemon is ready or after timeout
	go func() {
		select {
		case <-readyCh:
			slog.Info("Daemon ready")
		case <-time.After(30 * time.Second):
			slog.Warn("Daemon not ready after 30s, showing window anyway")
		case <-ctx.Done():
			return
		}
		window.Show()
		window.Focus()
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
