package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestControlServerDispatchesVerb(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wrapper.sock")
	cs := NewControlServer(sock)
	got := make(chan json.RawMessage, 1)
	cs.Handle("window.focus", func(params json.RawMessage) (any, error) {
		got <- params
		return map[string]string{"ok": "1"}, nil
	})
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Close()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sock)
		},
	}}
	resp, err := client.Post("http://wrapper/verb/window.focus", "application/json",
		bytes.NewReader([]byte(`{"x":1}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	select {
	case p := <-got:
		if string(p) != `{"x":1}` {
			t.Fatalf("params=%s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called")
	}
}

func TestControlServerUnknownVerb404(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wrapper.sock")
	cs := NewControlServer(sock)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Close()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sock)
		},
	}}
	resp, err := client.Post("http://wrapper/verb/nope", "application/json", http.NoBody)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}
