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
