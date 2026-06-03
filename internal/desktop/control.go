package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

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
	_ = os.Remove(c.sockPath) // stale socket from a previous run
	ln, err := net.Listen("unix", c.sockPath)
	if err != nil {
		return fmt.Errorf("listen wrapper socket: %w", err)
	}
	c.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("POST /verb/", c.handleVerb)
	c.srv = &http.Server{Handler: mux}
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
