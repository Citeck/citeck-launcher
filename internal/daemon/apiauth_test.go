package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func decodeErrBody(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorDto {
	t.Helper()
	var dto api.ErrorDto
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode error body: %v (body=%q)", err, rec.Body.String())
	}
	return dto
}

// --- Middleware ---

func TestAPIAuthMiddleware_NoToken401(t *testing.T) {
	h := newAPIAuth(testToken).Middleware(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if dto := decodeErrBody(t, rec); dto.Code != api.ErrCodeAuthRequired {
		t.Errorf("error code = %q, want %q", dto.Code, api.ErrCodeAuthRequired)
	}
}

func TestAPIAuthMiddleware_BearerOK(t *testing.T) {
	h := newAPIAuth(testToken).Middleware(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid bearer, got %d", rec.Code)
	}
}

func TestAPIAuthMiddleware_WrongBearer401(t *testing.T) {
	h := newAPIAuth(testToken).Middleware(okHandler())
	for _, header := range []string{
		"Bearer wrong-token",
		"Bearer ", // empty token
		"Basic " + testToken, // wrong scheme
		testToken, // bare token without scheme
	} {
		req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: expected 401, got %d", header, rec.Code)
		}
	}
}

func TestAPIAuthMiddleware_SessionCookieOK(t *testing.T) {
	auth := newAPIAuth(testToken)
	h := auth.Middleware(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/events", http.NoBody) // SSE path: cookie is the only option
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: auth.createSession()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with session cookie, got %d", rec.Code)
	}
}

func TestAPIAuthMiddleware_UnknownOrExpiredSession401(t *testing.T) {
	auth := newAPIAuth(testToken)
	h := auth.Middleware(okHandler())

	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "deadbeef"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown session: expected 401, got %d", rec.Code)
	}

	// Expired session is rejected and lazily removed.
	id := auth.createSession()
	auth.mu.Lock()
	auth.sessions[id] = time.Now().Add(-time.Minute)
	auth.mu.Unlock()
	req = httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: id})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired session: expected 401, got %d", rec.Code)
	}
	auth.mu.Lock()
	_, still := auth.sessions[id]
	auth.mu.Unlock()
	if still {
		t.Error("expired session should be pruned on access")
	}
}

func TestAPIAuthMiddleware_MTLSBypass(t *testing.T) {
	h := newAPIAuth(testToken).Middleware(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: "admin"}}},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("mTLS-authenticated request must bypass token auth, got %d", rec.Code)
	}
}

func TestAPIAuthMiddleware_StaticAndHandshakePublic(t *testing.T) {
	h := newAPIAuth(testToken).Middleware(okHandler())
	for _, path := range []string{"/", "/assets/index-abc.js", "/favicon.ico", "/welcome", api.AuthSession} {
		req := httptest.NewRequest("GET", path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %q should stay public (the API is what's protected), got %d", path, rec.Code)
		}
	}
}

// --- Transport wiring ---

// Disabled (apiAuth == nil) = today's behavior: the server TCP chain serves
// /api without any token.
func TestServerTCPHandler_DisabledIsTodaysBehavior(t *testing.T) {
	d := &Daemon{}
	h := d.serverTCPHandler(okHandler(), "127.0.0.1:7088", false)
	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("api_auth disabled: expected 200 without token, got %d", rec.Code)
	}
}

func TestServerTCPHandler_EnabledRequiresToken(t *testing.T) {
	d := &Daemon{apiAuth: newAPIAuth(testToken)}
	h := d.serverTCPHandler(okHandler(), "127.0.0.1:7088", false)

	// No token → 401 AUTH_REQUIRED (even for a POST without CSRF header:
	// auth answers before CSRF so the client sees the real problem).
	req := httptest.NewRequest("POST", "/api/v1/namespace/start", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	if dto := decodeErrBody(t, rec); dto.Code != api.ErrCodeAuthRequired {
		t.Errorf("error code = %q, want %q", dto.Code, api.ErrCodeAuthRequired)
	}

	// Bearer + CSRF header → 200 (CSRF still enforced after auth).
	req = httptest.NewRequest("POST", "/api/v1/namespace/start", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Citeck-CSRF", "1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with bearer+CSRF, got %d", rec.Code)
	}

	// Bearer WITHOUT the CSRF header on a mutating request → still 403:
	// token auth does not replace the CSRF gate.
	req = httptest.NewRequest("POST", "/api/v1/namespace/start", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 (CSRF) with bearer but no CSRF header, got %d", rec.Code)
	}
}

// The Unix-socket chain must NEVER enforce token auth, even when api_auth is
// enabled — socket access is gated by the 0600 file mode and the local CLI
// sends no token.
func TestUnixHandler_BypassesTokenAuth(t *testing.T) {
	d := &Daemon{apiAuth: newAPIAuth(testToken)}
	h := d.unixHandler(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/namespace", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("unix transport must bypass token auth, got %d", rec.Code)
	}
}

// --- Handshake handler ---

func TestHandleAuthSession_SetsCookieAndRedirects(t *testing.T) {
	d := &Daemon{apiAuth: newAPIAuth(testToken)}
	req := httptest.NewRequest("GET", api.AuthSession+"?token="+testToken, http.NoBody)
	rec := httptest.NewRecorder()
	d.handleAuthSession(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location = %q, want /", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookieName {
		t.Errorf("cookie name = %q, want %q", c.Name, sessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("session cookie must be SameSite=Strict")
	}
	if c.Path != "/" {
		t.Errorf("cookie path = %q, want /", c.Path)
	}
	// The minted session must be accepted by the middleware.
	if !d.apiAuth.validSession(c.Value) {
		t.Error("handshake cookie value should be a valid session")
	}
}

func TestHandleAuthSession_RejectsBadToken(t *testing.T) {
	d := &Daemon{apiAuth: newAPIAuth(testToken)}
	for _, q := range []string{"?token=wrong", ""} {
		req := httptest.NewRequest("GET", api.AuthSession+q, http.NoBody)
		rec := httptest.NewRecorder()
		d.handleAuthSession(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("query %q: expected 401, got %d", q, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("query %q: no cookie must be set on rejection", q)
		}
	}
}

func TestHandleAuthSession_DisabledRedirectsWithoutCookie(t *testing.T) {
	d := &Daemon{} // api_auth disabled
	req := httptest.NewRequest("GET", api.AuthSession, http.NoBody)
	rec := httptest.NewRecorder()
	d.handleAuthSession(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 with auth disabled (citeck ui link still works), got %d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("no cookie must be set when auth is disabled")
	}
}

// --- Token compare ---

// tokenMatches must be exact (constant-time compare of SHA-256 digests —
// functional checks here; the constant-time property comes from
// subtle.ConstantTimeCompare over fixed-length hashes).
func TestTokenMatches(t *testing.T) {
	a := newAPIAuth(testToken)
	if !a.tokenMatches(testToken) {
		t.Error("exact token must match")
	}
	for _, bad := range []string{
		"",
		testToken[:len(testToken)-1],        // truncated
		testToken + "0",                     // extended
		strings.ToUpper(testToken),          // case-flipped
		strings.Repeat("x", len(testToken)), // same length, different content
	} {
		if a.tokenMatches(bad) {
			t.Errorf("token %q must not match", bad)
		}
	}
}

// Session table stays bounded: minting beyond maxSessions evicts rather than
// growing without limit.
func TestCreateSession_Bounded(t *testing.T) {
	a := newAPIAuth(testToken)
	for range maxSessions + 10 {
		a.createSession()
	}
	a.mu.Lock()
	n := len(a.sessions)
	a.mu.Unlock()
	if n > maxSessions {
		t.Errorf("session table size = %d, want <= %d", n, maxSessions)
	}
}
