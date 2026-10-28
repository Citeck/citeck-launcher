//go:build desktop

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// reloadWebview re-requests the UI through the wrapper's proxy so the page is
// served by whichever daemon is running NOW.
//
// It deliberately does NOT call WebviewWindow.Reload(). In Wails v3 before
// 3.0.0 that method was implemented on Linux (webkit_web_view_load_uri) and
// Windows (execJS window.location.reload), but on macOS
// `macosWebviewWindow.reload` was a `//TODO: Implement` stub that logged a
// debug line and returned (3.0.0 implements it). Nothing reports the difference — Reload() succeeds
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
//
// ExecJS queues until the window is marked runtime-ready. This wrapper calls
// wailswin.MarkRuntimeReadyOnFirstLoad so it does not depend on the daemon's
// page providing the handshake. The v2.17.3 frontend already posts that message
// after subscribing to desktop events; the earlier missing-handshake diagnosis
// applies to the ece634d baseline, not to every v2.17.3 installation.
// See docs/desktop-webview.md for wrapper/daemon version compatibility.
func reloadWebview(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	window.ExecJS("window.location.reload()")
}
