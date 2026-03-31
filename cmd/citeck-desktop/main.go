package main

import (
	"context"
	"log"
	"log/slog"
	"runtime"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

var version = "dev"

const loadingHTML = `<!DOCTYPE html>
<html><head><style>
body{margin:0;height:100vh;display:flex;align-items:center;justify-content:center;
background:#1e1e1e;color:#888;font-family:system-ui,sans-serif;font-size:14px}
.loader{text-align:center}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%;animation:spin 1s linear infinite;margin:0 auto 12px}
@keyframes spin{to{transform:rotate(360deg)}}
</style></head><body><div class="loader"><div class="spinner"></div>Starting...</div></body></html>`

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

	// Start daemon in a background goroutine with restart loop.
	// TCP listener enabled — webview connects directly to daemon HTTP server.
	go desktop.RunDaemonLoop(ctx, desktop.DaemonOpts{
		Version: version,
		ReadyCh: readyCh,
	})

	app := application.New(application.Options{
		Name:        "Citeck Launcher",
		Description: "Citeck ECOS Platform Launcher",
		Icon:        icons.ApplicationDarkMode256,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			slog.Info("Wails shutting down, stopping daemon")
			cancel()
		},
	})

	// Create main window with loading screen — daemon URL is set after readyCh
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "Citeck Launcher",
		Width:  1280,
		Height: 800,
		HTML:   loadingHTML,
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: false,
		},
	})

	// Hide on close instead of quitting (minimize to tray)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	// System tray — SetLabel sets the D-Bus Id/Title used by StatusNotifierItem
	// on Linux (Cinnamon, KDE, GNOME with AppIndicator extension).
	// SetTooltip is a no-op on Linux in Wails v3 alpha, so SetLabel is essential
	// for the tray to register with a proper name instead of "Wails".
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

	// Navigate to daemon URL once ready (window already shows loading screen)
	go func() {
		select {
		case <-readyCh:
			slog.Info("Daemon ready, navigating to Web UI", "url", daemonURL)
		case <-time.After(30 * time.Second):
			slog.Warn("Daemon not ready after 30s, navigating anyway")
		case <-ctx.Done():
			return
		}
		window.SetURL(daemonURL)
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
