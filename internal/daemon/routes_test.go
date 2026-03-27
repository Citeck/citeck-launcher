package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
)

// --- 12a-1: CSRF Middleware tests ---

func TestCSRFMiddleware_BlocksPostWithoutHeader(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/api/v1/namespace/start", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without CSRF header: got %d, want 403", method, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), api.ErrCodeCSRFMissing) {
			t.Errorf("%s: response body should contain %s", method, api.ErrCodeCSRFMissing)
		}
	}
}

func TestCSRFMiddleware_AllowsWithHeader(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"POST", "PUT", "DELETE"} {
		req := httptest.NewRequest(method, "/api/v1/namespace/start", nil)
		req.Header.Set("X-Citeck-CSRF", "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s with CSRF header: got %d, want 200", method, rec.Code)
		}
	}
}

func TestCSRFMiddleware_AllowsGETWithoutHeader(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/namespace", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET without CSRF header: got %d, want 200", rec.Code)
	}
}

// --- 12a-3: validateSnapshotURL tests ---

func TestValidateSnapshotURL_BlocksPrivateIPs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1/snap.zip"},
		{"private-10", "http://10.0.0.1/snap.zip"},
		{"private-172", "http://172.16.0.1/snap.zip"},
		{"private-192", "http://192.168.1.1/snap.zip"},
		{"localhost", "http://localhost/snap.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSnapshotURL(tc.url)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.url)
			}
		})
	}
}

func TestValidateSnapshotURL_BlocksBadScheme(t *testing.T) {
	err := validateSnapshotURL("ftp://example.com/snap.zip")
	if err == nil {
		t.Error("expected error for ftp:// scheme")
	}
}

func TestValidateSnapshotURL_AllowsPublicHTTPS(t *testing.T) {
	err := validateSnapshotURL("https://example.com/snap.zip")
	if err != nil {
		t.Skipf("DNS resolution may not be available: %v", err)
	}
}

// --- 12a-5: Two-mux boundary test ---

// isJSONResponse checks whether the response came from an API handler (Content-Type: application/json)
// vs the mux default 404 or WebUI fallback (text/plain or text/html).
func isJSONResponse(rec *httptest.ResponseRecorder) bool {
	ct := rec.Header().Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

func TestTwoMuxBoundary_SocketOnlyRoutes(t *testing.T) {
	d := &Daemon{}

	socketMux := http.NewServeMux()
	tcpMux := http.NewServeMux()
	d.registerRoutes(socketMux, tcpMux)

	// Socket-only routes must NOT be handled by the API handler on tcpMux.
	// On tcpMux these fall through to either mux 405 (if GET exists for the path)
	// or WebUI fallback (which returns 404 for /api/ paths).
	// Either way, the API handler should NOT be invoked.
	// Note: DaemonShutdown is excluded because it spawns a goroutine with side effects.
	socketOnlyRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/apps/test/exec"},
		{"PUT", api.Config},
		{"PUT", "/api/v1/apps/test/config"},
		{"PUT", "/api/v1/apps/test/files/some/path"},
		{"POST", api.NamespaceReload},
	}

	for _, rt := range socketOnlyRoutes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		tcpMux.ServeHTTP(rec, req)
		// The API handler on tcpMux should NOT have processed this.
		// Mux returns 404/405, WebUI returns 404 for /api/ paths — all non-JSON.
		if isJSONResponse(rec) {
			t.Errorf("tcpMux %s %s: got JSON response (API handler ran, but route should be socket-only)", rt.method, rt.path)
		}
	}

	// Verify socket-only routes ARE handled on socketMux (JSON response from handler).
	for _, rt := range socketOnlyRoutes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		socketMux.ServeHTTP(rec, req)
		// Handler should run (may return 400/404 with JSON for nil runtime — that's fine).
		if !isJSONResponse(rec) {
			t.Errorf("socketMux %s %s: got non-JSON response (handler not registered)", rt.method, rt.path)
		}
	}
}

func TestTwoMuxBoundary_SharedRoutes(t *testing.T) {
	d := &Daemon{}

	socketMux := http.NewServeMux()
	tcpMux := http.NewServeMux()
	d.registerRoutes(socketMux, tcpMux)

	// Shared routes (safe for nil Daemon) should return JSON on both muxes.
	sharedRoutes := []struct {
		method string
		path   string
	}{
		{"GET", api.DaemonStatus},
		{"GET", api.Namespace},
		{"POST", api.NamespaceStart},
		{"POST", api.NamespaceStop},
	}

	for _, rt := range sharedRoutes {
		// socketMux
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		socketMux.ServeHTTP(rec, req)
		if !isJSONResponse(rec) {
			t.Errorf("socketMux %s %s: expected JSON response, got %q", rt.method, rt.path, rec.Header().Get("Content-Type"))
		}

		// tcpMux
		req2 := httptest.NewRequest(rt.method, rt.path, nil)
		rec2 := httptest.NewRecorder()
		tcpMux.ServeHTTP(rec2, req2)
		if !isJSONResponse(rec2) {
			t.Errorf("tcpMux %s %s: expected JSON response, got %q", rt.method, rt.path, rec2.Header().Get("Content-Type"))
		}
	}
}
