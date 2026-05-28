package daemon

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDesktopFocusHandler_InvokesRegisteredCallback(t *testing.T) {
	t.Cleanup(func() { SetDesktopFocusHandler(nil) })

	var calls atomic.Int32
	SetDesktopFocusHandler(func() { calls.Add(1) })

	d := &Daemon{}
	req := httptest.NewRequest(http.MethodPost, "/desktop/focus", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDesktopFocus(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("callback invoked %d times, want 1", calls.Load())
	}
}

func TestDesktopFocusHandler_NoCallback(t *testing.T) {
	t.Cleanup(func() { SetDesktopFocusHandler(nil) })
	SetDesktopFocusHandler(nil)
	d := &Daemon{}
	req := httptest.NewRequest(http.MethodPost, "/desktop/focus", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDesktopFocus(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
