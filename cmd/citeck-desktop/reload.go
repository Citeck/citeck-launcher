//go:build desktop

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// reloadWebview re-requests the UI through the wrapper's proxy so the page is
// served by whichever daemon is running NOW.
//
// It deliberately does NOT call WebviewWindow.Reload(). In the pinned Wails
// v3 alpha that method is implemented on Linux (webkit_web_view_load_uri) and
// Windows (execJS window.location.reload), but on macOS
// `macosWebviewWindow.reload` is a `//TODO: Implement` stub that logs a debug
// line and returns. Nothing reports the difference — Reload() succeeds
// silently on every platform — so an auto-update on macOS swapped the daemon
// underneath a page that was never told, leaving the update dialog stuck in
// its "installing" state with every button (Cancel included) disabled and the
// launcher unusable until the user quit and relaunched it. That was reported
// against 2.11.0, on the first release where auto-update was offered outside
// Linux.
//
// Driving the reload from JS is what Wails' own Windows implementation does,
// works on all three platforms, and — unlike loading `wails://` — keeps the
// SPA on its current route, which matters for the desktop child windows
// (/window/...). If the webview is gone or shutting down, ExecJS is a no-op,
// which is the correct outcome for a window nobody is looking at.
func reloadWebview(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	window.ExecJS("window.location.reload()")
}
