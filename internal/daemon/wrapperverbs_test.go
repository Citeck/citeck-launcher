package daemon

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestCallWrapperVerb(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wrapper.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	hit := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /verb/", func(w http.ResponseWriter, r *http.Request) {
		hit <- r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	wc := newWrapperClient(sock)
	if err := wc.call(context.Background(), "window.focus", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := <-hit; got != "/verb/window.focus" {
		t.Fatalf("path=%s", got)
	}
}

func TestCallWrapperVerbNoSocketIsNoop(t *testing.T) {
	wc := newWrapperClient("")
	if err := wc.call(context.Background(), "window.focus", nil); err != nil {
		t.Fatalf("empty-socket call should be a no-op, got %v", err)
	}
}
