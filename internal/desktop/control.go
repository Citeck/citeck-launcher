package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ctrlReadHeaderTimeout bounds request-header reads (Slowloris guard).
const ctrlReadHeaderTimeout = 5 * time.Second

// VerbHandler executes a native verb. params is the raw JSON body; the return
// value (if non-nil) is JSON-encoded back to the caller.
type VerbHandler func(params json.RawMessage) (any, error)

// ControlServer listens on the wrapper control socket and dispatches
// POST /verb/<name> to registered handlers. Handlers are injected so this file
// stays Wails-free and unit-testable; the GUI wires real handlers later.
type ControlServer struct {
	sockPath string
	mu       sync.RWMutex
	handlers map[string]VerbHandler
	ln       net.Listener
	srv      *http.Server
}

// NewControlServer creates a control server bound to sockPath (not yet started).
func NewControlServer(sockPath string) *ControlServer {
	return &ControlServer{sockPath: sockPath, handlers: map[string]VerbHandler{}}
}

// Handle registers a verb handler. Safe to call before Start.
func (c *ControlServer) Handle(verb string, h VerbHandler) {
	c.mu.Lock()
	c.handlers[verb] = h
	c.mu.Unlock()
}

// Verbs returns the registered verb names (for capabilities advertisement).
func (c *ControlServer) Verbs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.handlers))
	for v := range c.handlers {
		out = append(out, v)
	}
	return out
}

// Start binds the unix socket and serves in a background goroutine.
func (c *ControlServer) Start() error {
	// Ensure the socket's parent dir exists. The wrapper binds this control
	// socket BEFORE it launches the daemon (which is what normally creates the
	// run dir), so on a fresh install the wrapper is the first to touch it. A
	// missing dir makes net.Listen("unix", …) fail — on Windows it surfaces as
	// the opaque "bind: A socket operation encountered a dead network"
	// (WSAENETDOWN), which is why a clean Windows install would not start.
	if dir := filepath.Dir(c.sockPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // run dir needs 0o755 for the daemon child
			return fmt.Errorf("create wrapper socket dir: %w", err)
		}
	}
	_ = os.Remove(c.sockPath) // stale socket from a previous run
	ln, err := net.Listen("unix", c.sockPath)
	if err != nil {
		return fmt.Errorf("listen wrapper socket: %w", err)
	}
	c.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("POST /verb/", c.handleVerb)
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: ctrlReadHeaderTimeout}
	go func() { _ = c.srv.Serve(ln) }()
	return nil
}

func (c *ControlServer) handleVerb(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/verb/")
	c.mu.RLock()
	h := c.handlers[name]
	c.mu.RUnlock()
	if h == nil {
		http.Error(w, "unknown verb", http.StatusNotFound)
		return
	}
	var body json.RawMessage
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			body = b
		}
	}
	res, err := h(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// Close stops the server and removes the socket.
func (c *ControlServer) Close() {
	if c.srv != nil {
		_ = c.srv.Close()
	}
	_ = os.Remove(c.sockPath)
}
