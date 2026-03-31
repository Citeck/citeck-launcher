package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

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

	// Context for daemon lifecycle — cancelled when Wails quits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ReadyCh receives notification when daemon HTTP server is ready
	readyCh := make(chan string, 1)

	// Load daemon config to get listen address
	daemonCfg, _ := config.LoadDaemonConfig()
	listenAddr := daemonCfg.Server.WebUI.Listen // e.g. "127.0.0.1:7088"
	daemonURL := "http://" + listenAddr

	// Start daemon in background
	go desktop.RunDaemonLoop(ctx, desktop.DaemonOpts{
		Version: version,
		ReadyCh: readyCh,
	})

	// Reverse proxy to daemon — Wails AssetServer proxies all requests.
	// This makes Wails the webview origin, so /wails/runtime (Browser.OpenURL etc.) works natively.
	daemonTarget, _ := url.Parse(daemonURL)
	proxy := httputil.NewSingleHostReverseProxy(daemonTarget)
	// Don't log proxy errors before daemon is ready
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "daemon not ready", http.StatusBadGateway)
	}

	// Wait for daemon in a goroutine, set ready flag
	daemonReady := make(chan struct{})
	go func() {
		select {
		case <-readyCh:
			slog.Info("Daemon ready", "url", daemonURL)
		case <-time.After(30 * time.Second):
			slog.Warn("Daemon not ready after 30s, proxying anyway")
		case <-ctx.Done():
			return
		}
		close(daemonReady)
	}()

	// Loading page handler — served until daemon is ready
	loadingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-daemonReady:
			// Daemon ready — proxy the request
			proxy.ServeHTTP(w, r)
		default:
			// Not ready yet — serve loading page for HTML requests, 502 for API
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "daemon starting", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(loadingHTML))
		}
	})

	app := application.New(application.Options{
		Name:        "Citeck Launcher",
		Description: "Citeck ECOS Platform Launcher",
		Icon:        icons.ApplicationDarkMode256,
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
		Name:   "main",
		Title:  "Citeck Launcher",
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
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.ApplicationDarkMode256)
	}

	socketPath := config.SocketPath()
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

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

const loadingHTML = `<!DOCTYPE html>
<html><head><meta http-equiv="refresh" content="2"><style>
body{margin:0;height:100vh;display:flex;align-items:center;justify-content:center;
background:#1e1e1e;color:#888;font-family:system-ui,sans-serif;font-size:14px}
.loader{text-align:center}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%;animation:spin 1s linear infinite;margin:0 auto 12px}
@keyframes spin{to{transform:rotate(360deg)}}
</style></head><body><div class="loader"><div class="spinner"></div>Starting...</div></body></html>`
