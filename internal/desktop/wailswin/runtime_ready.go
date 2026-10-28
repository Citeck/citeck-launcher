//go:build (linux && gtk3) || darwin || windows

package wailswin

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// runtimeReadyMessage is what Wails' own JS runtime (@wailsio/runtime,
// /wails/runtime.js) posts once it has loaded. A window does not run anything
// sent through ExecJS until it has received this message: Wails appends it to
// a per-window queue and only flushes the queue in HandleMessage's
// "wails:runtime:ready" case. That queue is also the only path a Go-side event
// takes into the page — app.Event.Emit reaches every window as
// ExecJS("window._wails.dispatchWailsEvent(...)").
//
// The UI does not import that runtime, but can send this message directly:
// v2.17.3 does so after subscribing to desktop events. The older ece634d
// baseline did not, leaving wrapper JS queued. MarkRuntimeReadyOnFirstLoad
// makes this wrapper independent of the daemon-served page's version.
// See docs/desktop-webview.md; absence of the JS runtime alone is not proof
// that the handshake is missing.
const runtimeReadyMessage = "wails:runtime:ready"

// pageLoadEvents are the native "a page has finished loading" events, one per
// platform; a window only ever fires its own platform's. They are the same
// moments Wails picks to inject its runtime core (window._wails.invoke and
// friends) into the page.
var pageLoadEvents = []events.WindowEventType{
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
	events.Windows.WebViewNavigationCompleted,
}

// readyWindow is the part of application.Window MarkRuntimeReadyOnFirstLoad
// needs, so the tests can drive it without a running Wails application.
type readyWindow interface {
	Name() string
	RegisterHook(eventType events.WindowEventType, callback func(event *application.WindowEvent)) func()
	HandleMessage(message string)
}

// MarkRuntimeReadyOnFirstLoad tells Wails that win's page can take ExecJS once
// the window's first page has finished loading, standing in for the runtime the
// page does not load. Call it for EVERY webview window the wrapper creates,
// right after creating it: app.Event.Emit reaches all windows, and a page
// without its own handshake would otherwise keep queuing that traffic.
//
// The wrapper's JS is safe to run on whatever document is showing — events go
// through a guarded window._wails.dispatchWailsEvent call, and a reload is a
// reload — so the only thing worth waiting for is the first load: before it,
// ExecJS still queues, which keeps a reload from aborting the window's initial
// navigation. Once marked, a window stays marked across reloads (Wails never
// resets it), hence once per window.
//
// Doing this in the wrapper also covers older daemon pages without changing
// their frontend. Auto-update replaces the daemon, not this wrapper: v2.17.3's
// frontend handshake can activate an older wrapper's WindowRuntimeReady zoom
// hook, whose alpha.98 macOS implementation uses pinch magnification. Only a
// wrapper upgrade supplies Wails 3.0.0 page zoom; see docs/desktop-webview.md.
func MarkRuntimeReadyOnFirstLoad(win readyWindow) {
	var once sync.Once
	for _, ev := range pageLoadEvents {
		win.RegisterHook(ev, func(_ *application.WindowEvent) {
			once.Do(func() {
				win.HandleMessage(runtimeReadyMessage)
				slog.Info("Webview loaded; JS from the wrapper now runs", "window", win.Name())
			})
		})
	}
}
