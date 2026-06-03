package daemon

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleDesktopFocus_CallsWrapperWindowFocus(t *testing.T) {
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

	t.Setenv("CITECK_WRAPPER_SOCK", sock)
	d := &Daemon{}
	req := httptest.NewRequest(http.MethodPost, "/desktop/focus", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDesktopFocus(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	select {
	case p := <-hit:
		if p != "/verb/window.focus" {
			t.Fatalf("path = %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper window.focus not called")
	}
}

func TestHandleDesktopFocus_NoWrapperSocket(t *testing.T) {
	t.Setenv("CITECK_WRAPPER_SOCK", "")
	d := &Daemon{}
	req := httptest.NewRequest(http.MethodPost, "/desktop/focus", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDesktopFocus(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
