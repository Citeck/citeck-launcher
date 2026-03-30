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

	// Create main window pointing directly at the daemon HTTP server
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "Citeck Launcher",
		Width:  1280,
		Height: 800,
		Hidden: true,
		URL:    daemonURL,
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

	// Show window once daemon is ready or after timeout
	go func() {
		select {
		case <-readyCh:
			slog.Info("Daemon ready", "url", daemonURL)
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
