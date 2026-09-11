package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

// The daemon answers as soon as the PROCESS is alive, not when its boot has
// finished.
//
// The socket used to be bound at the very END of Start, after the namespace
// load (git pull + bundle resolve), the desktop orphan sweep, the
// dependency-migration crash recovery and the namespace start. Everything that
// wanted to ask "is this daemon alive?" could therefore only measure "how long
// did this whole boot take, including Docker I/O, git I/O and namespace
// startup" — and one of those askers is the desktop update health gate
// (desktop.UpdateHealthTimeout), which rolls a freshly applied daemon back when
// it does not answer in time. Measured on a Windows host whose Docker Desktop
// named pipe was dead (2026-09-08): the orphan sweep burned its full 90 s
// budget, readiness arrived 90 s after the process started, the 60 s gate
// failed, and a perfectly good 2.11.7 was rolled back to 2.11.5.
//
// So the listener is bound right after the single-instance guard and serves a
// tiny boot handler until the real routes are ready; then the handler is
// swapped ATOMICALLY on the same listener and the same socket file, so nothing
// reconnects and no second bind can race the first.
//
// Server mode gets the same treatment deliberately: a `citeck status` told
// plainly that the daemon is still starting is strictly better than a
// connection refused, and one boot order is easier to keep correct than two.

// bootRetryAfter is the Retry-After header value (seconds) sent with every
// DAEMON_STARTING refusal. One second: the client is local, and the only thing
// it is waiting for is this process finishing its own boot.
const bootRetryAfter = "1"

// switchableHandler is one http.Handler whose delegate can be replaced while
// requests are in flight. atomic.Pointer, not a mutex: the read is on every
// request and the write happens exactly once per daemon.
type switchableHandler struct {
	delegate atomic.Pointer[http.Handler]
}

func (s *switchableHandler) set(h http.Handler) {
	s.delegate.Store(&h)
}

func (s *switchableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.delegate.Load()).ServeHTTP(w, r)
}

// bootSocket is the Unix listener bound BEFORE the slow boot phase, together
// with the server that carries it from the boot handler to the real routes.
type bootSocket struct {
	listener net.Listener
	server   *http.Server
	routes   *switchableHandler
}

// bindBootSocket creates the daemon's Unix listener and the HTTP server that
// serves it, starting on the boot handler. The listener, the server and the
// socket file are the SAME ones the fully booted daemon uses — installRoutes
// only swaps what is behind them.
func bindBootSocket(socketPath string) (*bootSocket, error) {
	routes := &switchableHandler{}
	routes.set(bootHandler())

	srv := &http.Server{
		Handler:        unixHandlerChain(routes),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   120 * time.Second, // kcadm.sh exec can take 30-60s on slow hardware
		MaxHeaderBytes: 1 << 20,           // 1MB
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	// The 0600 mode IS the socket's access control (the Unix transport carries
	// no token auth for exactly that reason), so it is applied before anything
	// can be served through it.
	if chmodErr := os.Chmod(socketPath, 0o600); chmodErr != nil {
		slog.Warn("Failed to chmod socket", "path", socketPath, "err", chmodErr)
	}
	return &bootSocket{listener: listener, server: srv, routes: routes}, nil
}

// serve starts serving in the background and returns the channel carrying the
// server's terminal error (http.ErrServerClosed on a clean shutdown). Buffered,
// so a caller that abandons the boot never blocks the serve goroutine.
func (b *bootSocket) serve() <-chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- b.server.Serve(b.listener) }()
	return errCh
}

// installRoutes replaces the boot handler with the daemon's real routes. The
// middleware chain around it (recovery + logging) is untouched — it was built
// around the switch, not around the handler.
func (b *bootSocket) installRoutes(h http.Handler) {
	b.routes.set(h)
}

// close tears the early socket down when the boot never completed. Closing the
// server closes the listener, and a Unix listener created by net.Listen unlinks
// its socket file on close — so an aborted boot leaves nothing behind, where
// before this change a failed boot had simply never bound anything.
func (b *bootSocket) close() {
	_ = b.server.Close()
	_ = os.Remove(b.listener.Addr().String())
}

// bootLoadingPage is what the socket serves for a request that is not an API
// call while the daemon is still booting.
//
// It exists because of a REAL break, not for looks. An auto-update replaces the
// daemon binary only — the desktop wrapper stays the version the user
// installed — and a wrapper proxies whatever the daemon answers straight into
// the webview. So when this daemon began answering a document request with
// `503 DAEMON_STARTING`, a 2.12.0 wrapper running a 2.12.1 daemon put that JSON
// on screen and left it there, because nothing reloads a webview. Reported from
// the field as a white screen with JSON on it; a full reinstall (i.e. a new
// wrapper) was the only way out.
//
// Before the socket was bound early there was no such answer to proxy: the
// connection simply failed and the wrapper drew its own loading page. This
// restores that property from the daemon's side, where it holds for EVERY
// wrapper, old or new — the page refreshes itself, so the window arrives at the
// real UI a second after the boot ends.
//
// Deliberately: no words that would need translating (the daemon has no locale
// yet at this point in its boot — no config, no request context), the same dark
// spinner the wrapper's own page draws, and no assets, since nothing else is
// served yet.
const bootLoadingPage = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="1">
<title>Citeck Launcher</title><style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#1e1e1e}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%;animation:spin 1s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
</style></head><body><div class="spinner"></div></body></html>
`

// bootHandler is what the socket serves while the daemon is still booting. It
// touches NOTHING — no Docker, no store, no runtime — because being able to
// answer while all three are still being built is the entire point.
func bootHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == api.Health {
			// A liveness answer, not a health verdict: the real handleHealth
			// pings Docker with a 10 s timeout, which is one of the things that
			// can hang here.
			writeJSON(w, api.HealthDto{
				Status:  api.HealthStatusStarting,
				Healthy: false,
				Checks: []api.HealthCheckDto{{
					Name:    "daemon",
					Status:  api.HealthStatusStarting,
					Message: "the daemon process is up and still starting",
				}},
			})
			return
		}
		if !strings.HasPrefix(r.URL.Path, api.APIV1) {
			// Anything that is not an API call is a request for the WINDOW, and
			// a window is not something to refuse with JSON — see
			// bootLoadingPage for the white screen that answer produced on a
			// wrapper one release older than this daemon.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, bootLoadingPage)
			return
		}
		// API calls are refused honestly. Notably /daemon/status is NOT
		// answered with a synthetic "running": a client told the daemon is up
		// and then handed a 503 on its next call is worse off than one told
		// plainly to wait.
		w.Header().Set("Retry-After", bootRetryAfter)
		writeErrorCode(w, http.StatusServiceUnavailable, api.ErrCodeDaemonStarting,
			"the daemon is still starting; retry in a moment")
	})
}
