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
// Our web UI never loads that runtime (it has no use for its bindings), so the
// message never came: every reload, every bridged daemon event and every ping
// was appended to the queue for the life of the window and never executed.
// Measured on macOS before the fix: no WindowRuntimeReady in two minutes of a
// loaded UI, a requested reload still not done 78 s later, and none of the
// daemon's 10 s pings reaching the page — which therefore ran on the store's
// 3 s HTTP poll fallback, while the queue kept every app_stats event (one per
// running app every 5 s) in the wrapper's memory.
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
// right after creating it: app.Event.Emit reaches all windows, so a window left
// out keeps queuing the event bridge's traffic for as long as it is open.
//
// The wrapper's JS is safe to run on whatever document is showing — events go
// through a guarded window._wails.dispatchWailsEvent call, and a reload is a
// reload — so the only thing worth waiting for is the first load: before it,
// ExecJS still queues, which keeps a reload from aborting the window's initial
// navigation. Once marked, a window stays marked across reloads (Wails never
// resets it), hence once per window.
//
// Done from Go rather than by having the page post the message itself, because
// the page arrives from the daemon and an auto-update replaces the daemon but
// not the wrapper: a web UI that posted it would also reach every wrapper
// already installed, and those register a WindowRuntimeReady hook that applies
// UIZoom — on macOS under Wails alpha.98 that is setMagnification, the
// pinch-zoom that lays the page out wider than the window.
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
