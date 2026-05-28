package daemon

import (
	"net/http"
	"sync/atomic"
)

// desktopFocusHandler is a process-global atomic pointer so the desktop main
// process can register a "raise main window" callback after Wails is wired,
// even though daemon.Start runs in a separate goroutine and never returns the
// *Daemon instance. The atomic is nil in server mode and ignored unless the
// /desktop/focus route was registered (desktop mode only).
var desktopFocusHandler atomic.Pointer[func()]

// SetDesktopFocusHandler registers a callback invoked by POST /desktop/focus.
// Pass nil to clear (used by tests).
func SetDesktopFocusHandler(fn func()) {
	if fn == nil {
		desktopFocusHandler.Store(nil)
		return
	}
	desktopFocusHandler.Store(&fn)
}

func (d *Daemon) handleDesktopFocus(w http.ResponseWriter, _ *http.Request) {
	fn := desktopFocusHandler.Load()
	if fn == nil {
		http.Error(w, "no focus handler registered", http.StatusServiceUnavailable)
		return
	}
	(*fn)()
	w.WriteHeader(http.StatusNoContent)
}
