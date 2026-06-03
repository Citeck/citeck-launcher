package daemon

import (
	"context"
	"net/http"
	"os"
	"time"
)

// handleDesktopFocus raises the desktop wrapper's main window in response to a
// second-instance launch (Kotlin AppLocalSocket parity).
//
// The daemon now runs as a separate process from the Wails wrapper, so it can no
// longer invoke a process-global UI callback. Instead it calls the wrapper's
// control socket (CITECK_WRAPPER_SOCK) with the "window.focus" verb. The verb
// string is the literal value of desktop.VerbWindowFocus — the daemon package
// cannot import internal/desktop (that would be an import cycle), so the constant
// is duplicated here exactly as in desktop_tray.go.
//
// In server mode CITECK_WRAPPER_SOCK is unset → 503 (no wrapper to focus). If the
// wrapper socket is set but the call fails → 502 (wrapper unreachable). On
// success → 204.
func (d *Daemon) handleDesktopFocus(w http.ResponseWriter, r *http.Request) {
	sock := os.Getenv("CITECK_WRAPPER_SOCK")
	if sock == "" {
		http.Error(w, "no wrapper socket configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := newWrapperClient(sock).call(ctx, "window.focus", nil); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
