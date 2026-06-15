package daemon

// Opt-in bearer-token auth for the localhost TCP Web UI/API transport
// (daemon.yml `api_auth.enabled`, default false). Closes the audit gap where
// any local OS user could drive the privileged API: the Unix socket is
// already 0600 and non-localhost TCP requires mTLS, but a localhost TCP bind
// historically served the full API behind only a browser-CSRF gate.
//
// With api_auth enabled, every /api/* request arriving over TCP must present
// `Authorization: Bearer <token>` or the session cookie minted by the
// GET /auth/session?token=<token> handshake; otherwise the daemon answers
// 401 with code AUTH_REQUIRED. Static UI assets (index.html, JS/CSS, SPA
// routes) and /auth/session itself stay public — the API is what is
// privileged, and the shell must be able to render the token prompt.
//
// Bypass matrix (by construction, see bootstrap.go):
//   - Unix socket: middleware never installed (unixHandler) — CLI unaffected.
//   - Desktop wrapper TCP (CITECK_DESKTOP_TCP): desktop chain, token-free.
//   - mTLS-authenticated requests: client cert is stronger auth already.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

const (
	// sessionCookieName carries the browser session id minted by the
	// /auth/session handshake so the raw token never lives in the browser.
	sessionCookieName = "citeck_session"
	// sessionTTL bounds how long a browser session stays valid before the
	// user has to re-run `citeck ui` (or re-paste the token).
	sessionTTL = 24 * time.Hour
	// maxSessions bounds the in-memory session table; the oldest session is
	// evicted beyond it (sessions are cheap to re-establish).
	maxSessions = 100
)

// apiAuth holds the resolved API token (hashed) and the in-memory browser
// sessions. nil on the Daemon means token auth is disabled (default) and the
// TCP transport behaves exactly as before.
type apiAuth struct {
	tokenHash [sha256.Size]byte // sha256(token) → constant-time, length-independent compare
	mu        sync.Mutex
	sessions  map[string]time.Time // session id → expiry
}

func newAPIAuth(token string) *apiAuth {
	return &apiAuth{
		tokenHash: sha256.Sum256([]byte(token)),
		sessions:  make(map[string]time.Time),
	}
}

// tokenMatches compares a presented token against the configured one in
// constant time. Both sides are SHA-256 hashed first so the comparison cost
// is independent of attacker-controlled input length and no prefix-timing
// signal leaks from the real token.
func (a *apiAuth) tokenMatches(presented string) bool {
	h := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(h[:], a.tokenHash[:]) == 1
}

// authorized reports whether the request carries a valid bearer token or a
// live session cookie.
func (a *apiAuth) authorized(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok && a.tokenMatches(strings.TrimSpace(token)) {
			return true
		}
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		return a.validSession(c.Value)
	}
	return false
}

// validSession checks (and lazily expires) a session id.
func (a *apiAuth) validSession(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, id)
		return false
	}
	return true
}

// createSession mints a random session id valid for sessionTTL. Expired
// entries are pruned on every mint, and the oldest live session is evicted
// when the table would exceed maxSessions (bounded memory).
func (a *apiAuth) createSession() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])

	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for sid, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, sid)
		}
	}
	if len(a.sessions) >= maxSessions {
		oldestID, oldestExp := "", now.Add(2*sessionTTL)
		for sid, exp := range a.sessions {
			if exp.Before(oldestExp) {
				oldestID, oldestExp = sid, exp
			}
		}
		delete(a.sessions, oldestID)
	}
	a.sessions[id] = now.Add(sessionTTL)
	return id
}

// Middleware enforces token/session auth on /api/* requests. Installed ONLY
// on the server-mode TCP chain (see serverTCPHandler in bootstrap.go); the
// Unix socket and the desktop wrapper path never see it. mTLS-authenticated
// requests pass through — the verified client cert outranks the token. Note
// the SSE endpoint lives under /api/v1 and is therefore covered: EventSource
// cannot set an Authorization header, which is exactly what the session
// cookie is for.
func (a *apiAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			next.ServeHTTP(w, r)
			return
		}
		// Only the API is privileged. The static UI shell (and /auth/session,
		// which is outside /api/) stays public so the browser can render the
		// token prompt and perform the handshake.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if a.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeMiddlewareError(w, http.StatusUnauthorized, api.ErrCodeAuthRequired,
			"API token required — run `citeck ui` on the host for an authenticated link")
	})
}

// handleAuthSession is the browser handshake: GET /auth/session?token=<token>
// validates the API token (constant-time) and answers with an HttpOnly
// SameSite=Strict session cookie plus a redirect to /, so the browser holds
// a revocable short-lived session instead of the raw token. With api_auth
// disabled it just redirects — the URL printed by `citeck ui` works in both
// modes. The token travels as a query parameter, but LoggingMiddleware logs
// r.URL.Path only, so it never lands in the daemon log.
func (d *Daemon) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	auth := d.apiAuth
	if auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" || !auth.tokenMatches(token) {
		writeErrorCode(w, http.StatusUnauthorized, api.ErrCodeAuthRequired, "invalid or missing token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    auth.createSession(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	// The request URL carries the raw token; keep it out of the browser cache
	// (and thus history/Referer) so only the revocable session cookie persists.
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
