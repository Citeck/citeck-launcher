package launchermcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type browserCeremony struct {
	id, nonce, csrf string
	form            url.Values
}

func startBrowserCeremony(t *testing.T, h *oauthTestCore, service *Service) browserCeremony {
	t.Helper()
	form := h.authorizeForm()
	r := httptest.NewRequest("GET", canonicalIssuer(DefaultPort)+"/authorize?"+form.Encode(), http.NoBody)
	w := httptest.NewRecorder()
	service.Handler(DefaultPort).ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	// no-referrer makes a browser form POST send Origin: null, which our
	// same-origin completion gate correctly refuses. Both policies must agree.
	require.Equal(t, "same-origin", w.Header().Get("Referrer-Policy"))
	require.Contains(t, w.Body.String(), `<meta name="referrer" content="same-origin">`)
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.Contains(t, w.Body.String(), "This browser cannot approve access")
	require.Contains(t, w.Body.String(), "form.submit()")
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].HttpOnly)
	require.Empty(t, cookies[0].Domain)
	require.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	match := regexp.MustCompile(`name="csrf" value="([a-f0-9]{64})"`).FindStringSubmatch(w.Body.String())
	require.Len(t, match, 2)
	id := strings.TrimPrefix(cookies[0].Path, "/oauth/requests/")
	return browserCeremony{id: id, nonce: cookies[0].Value, csrf: match[1], form: form}
}
func browserRequest(service *Service, b browserCeremony, action, nonce, csrf, origin string) *httptest.ResponseRecorder {
	method := "GET"
	var body io.ReadCloser = http.NoBody
	if action == "finish" {
		method = "POST"
		body = io.NopCloser(strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
	}
	r := httptest.NewRequest(method, canonicalIssuer(DefaultPort)+pendingBrowserPath(b.id)+"/"+action, body)
	r.AddCookie(&http.Cookie{Name: oauthCookieName(), Value: nonce})
	if action == "finish" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	service.Handler(DefaultPort).ServeHTTP(w, r)
	return w
}
func TestOAuthBrowserCompletionAtomicOwnerProofAndReplay(t *testing.T) {
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	b := startBrowserCeremony(t, h, s)
	require.Equal(t, 409, browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort)).Code)
	require.Equal(t, 404, browserRequest(s, b, "status", "wrong", b.csrf, "").Code)
	require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, ""))
	pending, err := h.store.ListOAuthPending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, Observer, pending[0].Role)
	require.Equal(t, strings.ToUpper(b.id[:8]), pending[0].CorrelationCode)
	status := browserRequest(s, b, "status", b.nonce, b.csrf, "")
	require.JSONEq(t, `{"status":"approved"}`, status.Body.String())
	for _, attempt := range []struct{ nonce, csrf, origin string }{{b.nonce, "wrong", canonicalIssuer(DefaultPort)}, {b.nonce, b.csrf, ""}, {b.nonce, b.csrf, "http://127.0.0.1:19381"}, {"wrong", b.csrf, canonicalIssuer(DefaultPort)}} {
		require.NotEqual(t, 303, browserRequest(s, b, "finish", attempt.nonce, attempt.csrf, attempt.origin).Code)
	}
	require.Zero(t, testCount(t, h.store, "oauth_codes"))
	h.store.beforeCommit = func() error { return errors.New("disk refused") }
	failed := browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort))
	require.Equal(t, 503, failed.Code)
	require.Empty(t, failed.Header().Get("Location"))
	require.Zero(t, testCount(t, h.store, "oauth_codes"))
	h.store.beforeCommit = nil
	done := browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort))
	require.Equal(t, 303, done.Code, done.Body.String())
	callback, err := url.Parse(done.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, b.form.Get("state"), callback.Query().Get("state"))
	require.Equal(t, canonicalIssuer(DefaultPort), callback.Query().Get("iss"))
	require.NotEmpty(t, callback.Query().Get("code"))
	require.Equal(t, 409, browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort)).Code)
	token, err := h.provider.exchange(context.Background(), h.exchangeForm(callback.Query().Get("code")))
	require.NoError(t, err)
	grants, err := h.store.ListOAuthConnections(context.Background())
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, Observer, grants[0].Role)
	require.NoError(t, h.store.RevokeOAuthConnection(context.Background(), grants[0].ID))
	_, err = h.provider.inspect(context.Background(), token.GetAccessToken())
	require.Error(t, err)
	// Raw completion/code values never enter SQL; code storage is signatures only.
	raw, err := os.ReadFile(h.store.path + "-wal")
	require.NoError(t, err)
	for _, secret := range []string{b.nonce, b.csrf, callback.Query().Get("code")} {
		require.NotContains(t, string(raw), secret)
	}
}
func TestOAuthBrowserCompletionConcurrencyOnlyOneCode(t *testing.T) {
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	b := startBrowserCeremony(t, h, s)
	require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, Developer))
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			_, err := h.store.finishAuthorization(context.Background(), b.id, b.nonce, b.csrf)
			if err == nil {
				results <- 1
			} else {
				results <- 0
			}
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for result := range results {
		success += result
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, testCount(t, h.store, "oauth_codes"))
	require.Equal(t, 1, testCount(t, h.store, "oauth_grants"))
}
func TestOAuthPendingDenyExpireRestartDisablePort(t *testing.T) {
	for _, action := range []string{"deny", "expire", "restart", "disable", "port"} {
		t.Run(action, func(t *testing.T) {
			h := newOAuthTestCore(t)
			s := New(h.store, &fakeBackend{})
			defer s.Close()
			b := startBrowserCeremony(t, h, s)
			switch action {
			case "deny":
				require.NoError(t, h.store.DenyOAuthPending(context.Background(), b.id))
			case "expire":
				_, err := h.store.db.Exec("UPDATE oauth_pending SET created=created-301000000000,expiry=expiry-301000000000 WHERE id=?", b.id)
				require.NoError(t, err)
			case "restart":
				require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, Developer))
				h = h.restart(t)
				s = New(h.store, &fakeBackend{})
				defer s.Close()
			case "disable":
				require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, Developer))
				require.NoError(t, h.store.SetSettings(Settings{Port: DefaultPort}))
				require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
			case "port":
				require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort + 1}))
				require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
			}
			require.Error(t, h.store.ApproveOAuthPending(context.Background(), b.id, Developer))
			completed := browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort))
			if action == "port" {
				require.NotEqual(t, 303, completed.Code)
			} else {
				require.Equal(t, 303, completed.Code, completed.Body.String())
				callback, err := url.Parse(completed.Header().Get("Location"))
				require.NoError(t, err)
				require.Equal(t, "access_denied", callback.Query().Get("error"))
				require.Empty(t, callback.Query().Get("code"))
			}
			require.Zero(t, testCount(t, h.store, "oauth_codes"))
			require.Zero(t, testCount(t, h.store, "oauth_grants"))
		})
	}
}
func TestOAuthHTTPAuthorizationValidationNoUnvalidatedRedirect(t *testing.T) {
	cases := map[string]func(url.Values){
		"missing pkce": func(f url.Values) { f.Del("code_challenge") }, "plain": func(f url.Values) { f.Set("code_challenge_method", "plain") }, "bad challenge": func(f url.Values) { f.Set("code_challenge", strings.Repeat("!", 43)) }, "wrong scope": func(f url.Values) { f.Set("scope", "developer") }, "extra scope": func(f url.Values) { f.Set("scope", "mcp developer") }, "role": func(f url.Values) { f.Set("role", "developer") }, "missing resource": func(f url.Values) { f.Del("resource") }, "empty resource": func(f url.Values) { f.Set("resource", "") }, "resource repeated": func(f url.Values) { f.Add("resource", canonicalResource(DefaultPort)) }, "wrong issuer": func(f url.Values) { f.Set("resource", canonicalResource(DefaultPort+1)) }, "redirect path": func(f url.Values) { f.Set("redirect_uri", testOAuthCallback+"/other") }, "redirect remote": func(f url.Values) { f.Set("redirect_uri", "http://attacker.example/callback") }, "client": func(f url.Values) { f.Set("client_id", "other") }, "response": func(f url.Values) { f.Set("response_type", "token") },
	}
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	handler := s.Handler(DefaultPort)
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := h.authorizeForm()
			change(f)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", canonicalIssuer(DefaultPort)+"/authorize?"+f.Encode(), http.NoBody))
			require.Equal(t, 400, w.Code, w.Body.String())
			require.Empty(t, w.Header().Get("Location"))
		})
	}
	require.Zero(t, testCount(t, h.store, "oauth_pending"))
	require.Zero(t, testCount(t, h.store, "oauth_codes"))
}
func TestOAuthHTTPDCRMetadataStrictAndRateBounds(t *testing.T) {
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	handler := s.Handler(DefaultPort)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server"} {
		r := httptest.NewRequest("GET", canonicalIssuer(DefaultPort)+path, http.NoBody)
		r.Header.Set("Forwarded", "host=evil.example")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), canonicalIssuer(DefaultPort))
		require.NotContains(t, w.Body.String(), "evil.example")
		require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	}
	for _, raw := range []string{`{"redirect_uris":["http://example.com:32123/callback"]}`, `{"redirect_uris":["http://127.0.0.1:32123/callback#x"]}`, `{"redirect_uris":["http://user@127.0.0.1:32123/callback"]}`, `{"redirect_uris":["http://127.0.0.1:*/callback"]}`, `{"redirect_uris":["http://127.0.0.1:32123/callback"],"role":"developer"}`, `{"redirect_uris":["http://127.0.0.1:32123/callback"],"scope":"developer"}`, `{"redirect_uris":["http://127.0.0.1:32123/callback"],"grant_types":["password"]}`, `{"redirect_uris":["http://127.0.0.1:32123/callback"],"client_name":"a","client_name":"b"}`} {
		r := httptest.NewRequest("POST", canonicalIssuer(DefaultPort)+"/register", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	good := `{"redirect_uris":["http://127.0.0.1:32123/callback"],"client_name":"SDK client","token_endpoint_auth_method":"none","client_uri":"http://untrusted.example/","logo_uri":"http://untrusted.example/logo","software_id":"sdk"}`
	for i := range 5 {
		r := httptest.NewRequest("POST", canonicalIssuer(DefaultPort)+"/register", strings.NewReader(good))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if i < 4 {
			require.Equal(t, 201, w.Code, w.Body.String())
		} else {
			require.Equal(t, 429, w.Code)
		}
	}
	require.Equal(t, 5, testCount(t, h.store, "oauth_clients"))
	require.Zero(t, testCount(t, h.store, "oauth_grants"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", canonicalResource(DefaultPort), http.NoBody)
	handler.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
	require.Equal(t, `Bearer resource_metadata="`+canonicalIssuer(DefaultPort)+`/.well-known/oauth-protected-resource/mcp", scope="mcp"`, w.Header().Get("WWW-Authenticate"))
	r = httptest.NewRequest("GET", "http://localhost:19380/authorize?"+h.authorizeForm().Encode(), http.NoBody)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), canonicalResource(DefaultPort))
}

func TestOAuthS256ChallengeSize(t *testing.T) {
	sum := sha256.Sum256([]byte(testOAuthVerifier))
	require.Len(t, base64.RawURLEncoding.EncodeToString(sum[:]), 43)
}

func TestOAuthHTTPTokenCommitFailureWithholdsCredentials(t *testing.T) {
	h := newOAuthTestCore(t)
	code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
	require.NoError(t, err)
	service := New(h.store, &fakeBackend{})
	defer service.Close()
	form := h.exchangeForm(code)
	form["sdk_extension"] = []string{"ignored", "also ignored"}
	h.store.beforeCommit = func() error { return errors.New("write refused") }
	w := httptest.NewRecorder()
	service.Handler(DefaultPort).ServeHTTP(w, oauthHTTPRequest(canonicalIssuer(DefaultPort)+"/token", form))
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "access_token")
	require.NotContains(t, w.Body.String(), "refresh_token")
	h.store.beforeCommit = nil
	w = httptest.NewRecorder()
	service.Handler(DefaultPort).ServeHTTP(w, oauthHTTPRequest(canonicalIssuer(DefaultPort)+"/token", form))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "access_token")
}

func TestOAuthHTTPStorageCallbackFailureIsServerError(t *testing.T) {
	for _, flow := range []string{"code", "refresh"} {
		t.Run(flow, func(t *testing.T) {
			h := newOAuthTestCore(t)
			code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
			require.NoError(t, err)
			form := h.exchangeForm(code)
			if flow == "refresh" {
				first, e := h.provider.exchange(context.Background(), form)
				require.NoError(t, e)
				form = h.refreshForm(testRefresh(first))
			}
			service := New(h.store, &fakeBackend{})
			defer service.Close()
			_, err = h.store.db.Exec("CREATE TEMP TRIGGER test_refuse BEFORE INSERT ON oauth_refresh_current BEGIN SELECT RAISE(ABORT,'private SQL detail'); END")
			require.NoError(t, err)
			w := httptest.NewRecorder()
			service.Handler(DefaultPort).ServeHTTP(w, oauthHTTPRequest(canonicalIssuer(DefaultPort)+"/token", form))
			require.Equal(t, 500, w.Code, w.Body.String())
			require.JSONEq(t, `{"error":"server_error","error_description":"OAuth operation failed; retry later"}`, w.Body.String())
			for _, secret := range []string{"private SQL detail", "INSERT", "access_token", "refresh_token", code} {
				require.NotContains(t, w.Body.String(), secret)
			}
			_, err = h.store.db.Exec("DROP TRIGGER test_refuse")
			require.NoError(t, err)
			w = httptest.NewRecorder()
			service.Handler(DefaultPort).ServeHTTP(w, oauthHTTPRequest(canonicalIssuer(DefaultPort)+"/token", form))
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "access_token")
		})
	}
}

func TestOAuthHTTPCanceledAdmissionDoesNotCloseAuthority(t *testing.T) {
	for _, endpoint := range []string{"register", "authorize"} {
		t.Run(endpoint, func(t *testing.T) {
			h := newOAuthTestCore(t)
			_, existing := h.issue(t, Observer, time.Now().UTC())
			service := New(h.store, &fakeBackend{})
			defer service.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("GET", canonicalIssuer(DefaultPort)+"/authorize?"+h.authorizeForm().Encode(), http.NoBody)
			if endpoint == "register" {
				r = httptest.NewRequest("POST", canonicalIssuer(DefaultPort)+"/register", strings.NewReader(`{"redirect_uris":["http://127.0.0.1:32123/callback"]}`))
				r.Header.Set("Content-Type", "application/json")
			}
			h.store.beforeCommit = func() error { cancel(); return nil }
			w := httptest.NewRecorder()
			service.Handler(DefaultPort).ServeHTTP(w, r.WithContext(ctx))
			require.Equal(t, 503, w.Code, w.Body.String())
			require.Empty(t, w.Header().Get("Location"))
			require.NotContains(t, w.Body.String(), "client_id")
			require.Zero(t, testCount(t, h.store, "oauth_pending"))
			require.Equal(t, 1, testCount(t, h.store, "oauth_clients"))
			require.False(t, h.store.uncertain)
			h.store.beforeCommit = nil
			_, err := h.provider.inspect(context.Background(), existing.GetAccessToken())
			require.NoError(t, err)
			_, _, _, err = h.store.beginAuthorization(context.Background(), h.authorizeForm())
			require.NoError(t, err)
		})
	}
}

func TestOAuthBrowserPollingReservesCompletion(t *testing.T) {
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	b := startBrowserCeremony(t, h, s)
	for range 240 {
		require.Equal(t, 200, browserRequest(s, b, "status", b.nonce, b.csrf, "").Code)
	}
	refused := browserRequest(s, b, "status", b.nonce, b.csrf, "")
	require.Equal(t, 429, refused.Code)
	require.Equal(t, "60", refused.Header().Get("Retry-After"))
	require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, ""))
	require.Equal(t, 303, browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort)).Code)
	// Replay stays refused, independently of polling admission.
	for range 119 {
		require.Equal(t, 409, browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort)).Code)
	}
	refused = browserRequest(s, b, "finish", b.nonce, b.csrf, canonicalIssuer(DefaultPort))
	require.Equal(t, 429, refused.Code)
	require.Equal(t, "60", refused.Header().Get("Retry-After"))
	require.Equal(t, 1, testCount(t, h.store, "oauth_codes"))
}

func TestOAuthBrowserMalformedAdmissionHasFixedCardinality(t *testing.T) {
	h := newOAuthTestCore(t)
	s := New(h.store, &fakeBackend{})
	defer s.Close()
	handler := s.Handler(DefaultPort)
	for i := range 121 {
		path := "/oauth/requests/" + strings.Repeat("a", 64) + "/status/" + strconv.Itoa(i)
		r := httptest.NewRequest("GET", canonicalIssuer(DefaultPort)+path, http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if i < 120 {
			require.Equal(t, 404, w.Code)
		} else {
			require.Equal(t, 429, w.Code)
			require.Equal(t, "60", w.Header().Get("Retry-After"))
		}
	}
	require.Len(t, s.oauthRates, 1)
	require.Contains(t, s.oauthRates, "browser-invalid")
	// Exact valid routes with wrong methods remain admitted then refused, and
	// cannot create keys from ids or suffixes.
	for _, action := range []string{"status", "finish"} {
		r := httptest.NewRequest("DELETE", canonicalIssuer(DefaultPort)+"/oauth/requests/"+strings.Repeat("b", 64)+"/"+action, http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 405, w.Code)
	}
	require.Len(t, s.oauthRates, 3)
	for _, path := range []string{"/oauth/requests/short/status", "/oauth/requests/" + strings.Repeat("a", 64) + "/finish/", "/prefix/oauth/requests/" + strings.Repeat("a", 64) + "/status"} {
		_, _, valid := browserOAuthRoute(path)
		require.False(t, valid, path)
	}
}
