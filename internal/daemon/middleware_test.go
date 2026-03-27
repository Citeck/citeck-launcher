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
	var capturedCN string
	handler := MTLSIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify we can read the CN from within the handler chain
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			capturedCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{
			{Subject: pkix.Name{CommonName: "admin-user"}},
		},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if capturedCN != "admin-user" {
		t.Errorf("expected CN 'admin-user', got %q", capturedCN)
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

func TestMTLSIdentityMiddleware_EmptyPeerCerts(t *testing.T) {
	handler := MTLSIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestBuildCORSAllowedOrigins(t *testing.T) {
	// Exact listen address — only that host:port is allowed
	origins := buildCORSAllowedOrigins("127.0.0.1:8088")
	if !origins["http://127.0.0.1:8088"] {
		t.Error("expected http://127.0.0.1:8088 to be allowed")
	}
	if origins["http://127.0.0.1:5173"] {
		t.Error("different port should not be allowed")
	}
	if origins["http://localhost:8088"] {
		t.Error("localhost should not match when bound to 127.0.0.1")
	}

	// Wildcard listen (0.0.0.0) — both localhost and 127.0.0.1 with exact port
	origins2 := buildCORSAllowedOrigins("0.0.0.0:8088")
	if !origins2["http://127.0.0.1:8088"] {
		t.Error("expected http://127.0.0.1:8088 for wildcard bind")
	}
	if !origins2["http://localhost:8088"] {
		t.Error("expected http://localhost:8088 for wildcard bind")
	}
	if origins2["http://localhost:3000"] {
		t.Error("different port should not be allowed on wildcard")
	}
}

func TestCORSMiddleware_RejectsOtherPort(t *testing.T) {
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "127.0.0.1:8088")

	// Same origin — no CORS header needed (browser same-origin)
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8088")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8088" {
		t.Error("expected CORS allow for exact listen origin")
	}

	// Different port — should NOT get CORS header
	req2 := httptest.NewRequest("GET", "/api/v1/status", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected CORS reject for different port")
	}

	// Evil origin
	req3 := httptest.NewRequest("GET", "/api/v1/status", nil)
	req3.Header.Set("Origin", "http://localhost.evil.com:8088")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected CORS reject for evil origin")
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
