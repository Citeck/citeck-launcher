package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMTLSIdentityMiddleware_WithCert(t *testing.T) {
	handler := MTLSIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{
			{Subject: pkix.Name{CommonName: "admin"}},
		},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMTLSIdentityMiddleware_NoCert(t *testing.T) {
	handler := MTLSIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (no TLS = pass through), got %d", rec.Code)
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
