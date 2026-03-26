package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenAuth_NoTokenConfigured(t *testing.T) {
	handler := TokenAuthMiddleware("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when no token configured, got %d", rec.Code)
	}
}

func TestTokenAuth_ValidToken(t *testing.T) {
	handler := TokenAuthMiddleware("secret123", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	req.RemoteAddr = "192.168.1.1:45678" // TCP connection
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d", rec.Code)
	}
}

func TestTokenAuth_InvalidToken(t *testing.T) {
	handler := TokenAuthMiddleware("secret123", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	req.RemoteAddr = "192.168.1.1:45678"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with invalid token, got %d", rec.Code)
	}
}

func TestTokenAuth_NoTokenOnTCP(t *testing.T) {
	handler := TokenAuthMiddleware("secret123", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.RemoteAddr = "192.168.1.1:45678"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token on TCP, got %d", rec.Code)
	}
}

func TestMatchCORSOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost", true},
		{"http://localhost:5173", true},
		{"http://127.0.0.1", true},
		{"http://127.0.0.1:8088", true},
		{"http://localhost.evil.com", false},
		{"http://127.0.0.1.evil.com", false},
		{"https://example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		got := matchCORSOrigin(tt.origin)
		if got != tt.want {
			t.Errorf("matchCORSOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	handler := RateLimitMiddleware(2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests should succeed (burst = rps)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, w.Code)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: got %d, want 429", w.Code)
	}

	// Different IP should not be limited
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "10.0.0.1:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("different IP: got %d, want 200", w2.Code)
	}
}

func TestTokenAuth_UnixSocketSkipsAuth(t *testing.T) {
	handler := TokenAuthMiddleware("secret123", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.RemoteAddr = "" // Unix socket has empty remote addr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for Unix socket (skip auth), got %d", rec.Code)
	}
}
