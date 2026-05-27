package desktop

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func startStubSocketServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stub.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return sockPath
}

func TestNotifyExistingInstance_Success(t *testing.T) {
	var called atomic.Int32
	sockPath := startStubSocketServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.URL.Path != "/desktop/focus" {
			t.Errorf("want /desktop/focus, got %s", r.URL.Path)
		}
		called.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := NotifyExistingInstance(sockPath); err != nil {
		t.Fatalf("NotifyExistingInstance: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", called.Load())
	}
}

func TestNotifyExistingInstance_ErrorStatus(t *testing.T) {
	sockPath := startStubSocketServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	err := NotifyExistingInstance(sockPath)
	if err == nil {
		t.Fatal("want error for 503, got nil")
	}
}

func TestNotifyExistingInstance_NoDaemon(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")
	err := NotifyExistingInstance(sockPath)
	if err == nil {
		t.Fatal("want error when no daemon listening, got nil")
	}
}
