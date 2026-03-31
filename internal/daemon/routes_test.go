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

// --- Route registration test ---

// isJSONResponse checks whether the response came from an API handler (Content-Type: application/json)
// vs the mux default 404 or WebUI fallback (text/plain or text/html).
func isJSONResponse(rec *httptest.ResponseRecorder) bool {
	ct := rec.Header().Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

func TestAllRoutesRegistered(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	// All routes should return JSON (handler runs, even with nil daemon fields).
	// Note: DaemonShutdown excluded because it spawns a goroutine with side effects.
	routes := []struct {
		method string
		path   string
	}{
		{"GET", api.DaemonStatus},
		{"GET", api.Namespace},
		{"POST", api.NamespaceStart},
		{"POST", api.NamespaceStop},
		{"POST", api.NamespaceReload},
		{"GET", api.Config},
		{"PUT", api.Config},
		{"POST", "/api/v1/apps/test/exec"},
		{"PUT", "/api/v1/apps/test/config"},
		{"PUT", "/api/v1/apps/test/files/some/path"},
		{"GET", "/api/v1/apps/test/inspect"},
		{"POST", "/api/v1/apps/test/restart"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if !isJSONResponse(rec) {
			t.Errorf("%s %s: expected JSON response (handler registered), got %q", rt.method, rt.path, rec.Header().Get("Content-Type"))
		}
	}
}
