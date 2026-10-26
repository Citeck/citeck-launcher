//go:build (linux && gtk3) || darwin || windows

package wailswin

import (
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// fakeReadyWindow records what MarkRuntimeReadyOnFirstLoad asks of a window;
// fire stands in for Wails dispatching a native window event to its hooks.
type fakeReadyWindow struct {
	mu       sync.Mutex
	hooks    map[events.WindowEventType][]func(*application.WindowEvent)
	messages []string
}

func newFakeReadyWindow() *fakeReadyWindow {
	return &fakeReadyWindow{hooks: map[events.WindowEventType][]func(*application.WindowEvent){}}
}

func (f *fakeReadyWindow) Name() string { return "fake" }

func (f *fakeReadyWindow) RegisterHook(ev events.WindowEventType, cb func(*application.WindowEvent)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks[ev] = append(f.hooks[ev], cb)
	return func() {}
}

func (f *fakeReadyWindow) HandleMessage(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
}

func (f *fakeReadyWindow) fire(ev events.WindowEventType) {
	f.mu.Lock()
	hooks := slices.Clone(f.hooks[ev])
	f.mu.Unlock()
	for _, cb := range hooks {
		cb(nil)
	}
}

func (f *fakeReadyWindow) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.messages)
}

// Each platform reports a finished page load with its own event, and a window
// only ever fires its own platform's — so every one of them has to release the
// window, and nothing may be released before the first load.
func TestRuntimeIsMarkedReadyOnEachPlatformsFirstPageLoad(t *testing.T) {
	for name, ev := range map[string]events.WindowEventType{
		"macOS":   events.Mac.WebViewDidFinishNavigation,
		"Linux":   events.Linux.WindowLoadFinished,
		"Windows": events.Windows.WebViewNavigationCompleted,
	} {
		t.Run(name, func(t *testing.T) {
			win := newFakeReadyWindow()
			MarkRuntimeReadyOnFirstLoad(win)
			require.Empty(t, win.received(), "nothing may run before the page has loaded")

			win.fire(ev)
			require.Equal(t, []string{"wails:runtime:ready"}, win.received())
		})
	}
}

// Wails never resets a window's ready flag, so reloads and later navigations
// have nothing to release; the window is marked exactly once.
func TestRuntimeIsMarkedReadyOncePerWindow(t *testing.T) {
	win := newFakeReadyWindow()
	MarkRuntimeReadyOnFirstLoad(win)

	for range 3 {
		win.fire(events.Mac.WebViewDidFinishNavigation)
	}
	win.fire(events.Windows.WebViewNavigationCompleted)

	require.Equal(t, []string{runtimeReadyMessage}, win.received())
}

// Events unrelated to a page load — above all WindowRuntimeReady itself, which
// marking ready makes Wails emit — must not mark anything.
func TestRuntimeIsNotMarkedReadyByOtherWindowEvents(t *testing.T) {
	win := newFakeReadyWindow()
	MarkRuntimeReadyOnFirstLoad(win)

	win.fire(events.Common.WindowRuntimeReady)
	win.fire(events.Common.WindowShow)
	win.fire(events.Mac.WebViewDidCommitNavigation)

	require.Empty(t, win.received())
}
