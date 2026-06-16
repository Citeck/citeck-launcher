//go:build (linux && gtk3) || darwin || windows

// Package wailswin owns the Wails secondary-window manager. It is split from
// internal/desktop so that the larger desktop package can be unit-tested
// without pulling in Wails (and therefore GTK) build dependencies.
//
// On Linux the package only compiles with `-tags gtk3` because the GTK4 path
// of Wails v3 alpha.96+ requires webkitgtk-6.0 packages that are not standard
// on Ubuntu 24.04. macOS and Windows builds are unaffected.
package wailswin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// UIZoom is the desktop webview zoom applied to every window. It uses the
// webview's NATIVE zoom (WebKitGTK set_zoom_level / WebView2 PutZoomFactor /
// WKWebView setMagnification), which scales the whole UI uniformly (text +
// icons + everything) and composes on top of the OS DPI scaling — so a Windows
// install at 125% and this zoom multiply correctly. The 2.x web UI read
// noticeably smaller than the 1.x launcher; this restores a comparable size.
// It's native (not a CSS hack), so it stays transparent to JS layout
// (getBoundingClientRect / pointer coords keep their CSS-px values), which
// keeps the virtualized app table and the bottom-panel drag working. Tune here.
const UIZoom = 1.1

// WindowSpec fields match the equivalent fields of [application.WebviewWindowOptions]
// so the frontend can stay close to the Wails vocabulary.
type WindowSpec struct {
	// Kind groups windows by purpose: "logs", "editor", etc.
	// The kind is part of the synthetic window name so that opening a
	// second window with the same kind+id focuses the existing one.
	Kind string `json:"kind"`

	// ID disambiguates windows of the same Kind. For "logs" it is the
	// app name; for "editor" it is "<app>/<file>"; etc.
	ID string `json:"id"`

	// Route is the SPA path to load, e.g. "/window/logs/eapps".
	// If empty, defaults to "/window/<kind>/<id>".
	Route string `json:"route"`

	// Title is displayed in the OS title bar.
	Title string `json:"title"`

	Width  int `json:"width"`
	Height int `json:"height"`
}

// WindowManager owns the Wails application reference and keeps track of
// spawned secondary windows so that repeat "Open Logs" clicks focus the
// existing window instead of stacking duplicates.
type WindowManager struct {
	app *application.App

	mu      sync.Mutex
	windows map[string]application.Window // key = synthetic name (kind|id)
}

// NewWindowManager wires the manager to a running Wails application.
func NewWindowManager(app *application.App) *WindowManager {
	return &WindowManager{
		app:     app,
		windows: make(map[string]application.Window),
	}
}

// HTTPHandler returns a handler for /desktop/windows/* endpoints.
// Mount it on the same asset server that serves the SPA so the frontend
// can hit it with a same-origin fetch (no CORS, no auth plumbing).
func (m *WindowManager) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /open", m.handleOpen)
	mux.HandleFunc("POST /close", m.handleClose)
	mux.HandleFunc("POST /close-all", m.handleCloseAll)
	mux.HandleFunc("POST /focus", m.handleFocus)
	mux.HandleFunc("GET /list", m.handleList)
	return mux
}

func (m *WindowManager) handleOpen(w http.ResponseWriter, r *http.Request) {
	var spec WindowSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.ID = strings.TrimSpace(spec.ID)
	if spec.Kind == "" {
		http.Error(w, "kind is required", http.StatusBadRequest)
		return
	}
	name := windowKey(spec.Kind, spec.ID)
	if spec.Route == "" {
		spec.Route = defaultRoute(spec.Kind, spec.ID)
	}
	if spec.Title == "" {
		spec.Title = defaultTitle(spec.Kind, spec.ID)
	}
	// Kotlin parity (LogsWindow / EditorWindow opened at 90% of the active
	// screen). If the JSON payload doesn't override Width/Height, derive them
	// from the primary screen's work area; otherwise fall back to per-kind
	// defaults so non-GTK builds and tests stay deterministic.
	autoSized := spec.Width == 0 && spec.Height == 0
	if spec.Width == 0 {
		spec.Width = defaultWidth(spec.Kind)
	}
	if spec.Height == 0 {
		spec.Height = defaultHeight(spec.Kind)
	}

	m.mu.Lock()
	existing, ok := m.windows[name]
	m.mu.Unlock()
	if ok && existing != nil {
		application.InvokeAsync(func() {
			existing.Show()
			existing.Focus()
		})
		writeJSON(w, http.StatusOK, map[string]string{"name": name, "reused": "true"})
		return
	}

	created := make(chan application.Window, 1)
	application.InvokeAsync(func() {
		width, height := spec.Width, spec.Height
		var targetScreen *application.Screen
		if autoSized {
			targetScreen, width, height = applyAutoSize(spec.Kind, width, height)
		}
		win := m.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:            name,
			Title:           spec.Title,
			URL:             spec.Route,
			Width:           width,
			Height:          height,
			InitialPosition: application.WindowCentered,
			Screen:          targetScreen,
			DevToolsEnabled: true,
			Zoom:            UIZoom,
			// F12 opens DevTools per-window (logs / editor windows are full
			// webviews, so the shortcut has to be registered on each).
			KeyBindings: map[string]func(application.Window){
				"F12": func(w application.Window) { w.OpenDevTools() },
			},
		})
		// Re-apply on macOS, where the init path skips options.Zoom (the
		// no-op elsewhere is harmless). Runs on the UI thread inside InvokeAsync.
		win.SetZoom(UIZoom)
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			m.mu.Lock()
			delete(m.windows, name)
			m.mu.Unlock()
		})
		created <- win
	})
	win := <-created
	m.mu.Lock()
	m.windows[name] = win
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "reused": "false"})
}

func (m *WindowManager) handleClose(w http.ResponseWriter, r *http.Request) {
	var spec WindowSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := windowKey(spec.Kind, spec.ID)
	m.mu.Lock()
	win, ok := m.windows[name]
	if ok {
		delete(m.windows, name)
	}
	m.mu.Unlock()
	if !ok || win == nil {
		http.Error(w, "no such window", http.StatusNotFound)
		return
	}
	application.InvokeAsync(func() { win.Close() })
	w.WriteHeader(http.StatusNoContent)
}

// handleCloseAll closes every tracked secondary window. The frontend calls it
// when navigating back to the Welcome screen (Kotlin parity:
// WorkspaceServices.setSelectedNamespace → CiteckWindow.closeAll), so logs /
// editor windows tied to the previous namespace don't linger. Idempotent: a
// no-op when no windows are open, so the frontend can fire it unconditionally
// on Welcome mount.
func (m *WindowManager) handleCloseAll(w http.ResponseWriter, _ *http.Request) {
	m.CloseAll()
	w.WriteHeader(http.StatusNoContent)
}

func (m *WindowManager) handleFocus(w http.ResponseWriter, r *http.Request) {
	var spec WindowSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := windowKey(spec.Kind, spec.ID)
	m.mu.Lock()
	win, ok := m.windows[name]
	m.mu.Unlock()
	if !ok || win == nil {
		http.Error(w, "no such window", http.StatusNotFound)
		return
	}
	application.InvokeAsync(func() {
		win.Show()
		win.Focus()
	})
	w.WriteHeader(http.StatusNoContent)
}

func (m *WindowManager) handleList(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	out := make([]string, 0, len(m.windows))
	for name := range m.windows {
		out = append(out, name)
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func windowKey(kind, id string) string {
	if id == "" {
		return kind
	}
	return kind + "|" + id
}

func defaultRoute(kind, id string) string {
	if id == "" {
		return "/window/" + kind
	}
	return "/window/" + kind + "/" + id
}

func defaultTitle(kind, id string) string {
	switch kind {
	case "logs":
		if id == "" {
			return "Logs"
		}
		return "Logs — " + id
	case "editor":
		if id == "" {
			return "Editor"
		}
		return "Editor — " + id
	case "daemon-logs":
		return "Launcher Logs"
	}
	display := capitalize(kind)
	if id == "" {
		return display
	}
	return display + " — " + id
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	first := strings.ToUpper(s[:1])
	return first + s[1:]
}

// percentOfScreen returns p% of the screen's WorkArea (excludes OS taskbars).
// Returns (0,0) if the screen reports zero bounds so the caller falls back.
func percentOfScreen(s *application.Screen, p int) (width, height int) {
	if s == nil {
		return 0, 0
	}
	w := s.WorkArea.Width
	h := s.WorkArea.Height
	if w <= 0 || h <= 0 {
		w = s.Size.Width
		h = s.Size.Height
	}
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	return w * p / 100, h * p / 100
}

// applyAutoSize selects the primary screen, applies per-kind Kotlin-parity
// sizing rules, and returns the target screen plus adjusted dimensions.
// Returns (nil, w, h) unchanged when no screen is available.
//
// Sizing rules:
//   - logs/daemon-logs → 100% work-area width, 90% height (long lines benefit
//     from maximum horizontal space)
//   - editor → min(default, screen*0.9): never inflate to 90% on a 4K monitor
func applyAutoSize(kind string, w, h int) (screen *application.Screen, width, height int) {
	s := application.GetScreenByIndex(0)
	if s == nil {
		return nil, w, h
	}
	switch kind {
	case "logs", "daemon-logs":
		if sw, sh := workAreaSize(s); sw > 0 && sh > 0 {
			w = sw
			h = sh * 90 / 100
		}
	default:
		if w90, h90 := percentOfScreen(s, 90); w90 > 0 && h90 > 0 {
			if w90 < w {
				w = w90
			}
			if h90 < h {
				h = h90
			}
		}
	}
	return s, w, h
}

// workAreaSize returns the screen's work area (excluding taskbar/dock) so
// fullscreen-ish windows don't overlap system chrome. Falls back to total
// size on platforms where WorkArea is unreported.
func workAreaSize(s *application.Screen) (width, height int) {
	w := s.WorkArea.Width
	h := s.WorkArea.Height
	if w <= 0 || h <= 0 {
		w = s.Size.Width
		h = s.Size.Height
	}
	return w, h
}

func defaultWidth(kind string) int {
	switch kind {
	case "logs", "daemon-logs":
		return 1100
	case "editor":
		return 1200
	default:
		return 900
	}
}

func defaultHeight(kind string) int {
	switch kind {
	case "logs", "daemon-logs":
		return 750
	case "editor":
		return 900
	default:
		return 700
	}
}

// CloseAll closes every tracked secondary window. Shared by app shutdown, the
// main-window close-to-tray hook, and the /close-all endpoint (back-to-Welcome).
func (m *WindowManager) CloseAll() {
	m.mu.Lock()
	windows := make([]application.Window, 0, len(m.windows))
	for _, win := range m.windows {
		windows = append(windows, win)
	}
	m.windows = make(map[string]application.Window)
	m.mu.Unlock()
	for _, win := range windows {
		application.InvokeAsync(func() { win.Close() })
	}
}

// OSDescription returns "linux/amd64" etc. – used by callers that want to
// log a platform hint together with WindowManager state.
func OSDescription() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
