package launchermcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const testOAuthCallback = "http://127.0.0.1:32123/callback"
const testOAuthVerifier = "strong-test-verifier-0000000000000000000000000000000000"

type oauthTestCore struct {
	store    *Store
	provider *oauthProvider
	client   oauthClient
}

func newOAuthTestCore(t *testing.T) *oauthTestCore {
	t.Helper()
	s, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	client, err := s.oauth.registerClient(context.Background(), "Unverified test client", []string{testOAuthCallback})
	require.NoError(t, err)
	return &oauthTestCore{store: s, provider: s.oauth, client: client}
}
func (h *oauthTestCore) authorizeForm() url.Values {
	sum := sha256.Sum256([]byte(testOAuthVerifier))
	return url.Values{"client_id": {h.client.ID}, "redirect_uri": {testOAuthCallback}, "response_type": {"code"}, "scope": {oauthScope}, "state": {"random-client-state-00000000000000"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {canonicalResource(h.store.Settings().Port)}}
}
func (h *oauthTestCore) exchangeForm(code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {h.client.ID}, "redirect_uri": {testOAuthCallback}, "code": {code}, "code_verifier": {testOAuthVerifier}, "resource": {canonicalResource(h.store.Settings().Port)}}
}
func (h *oauthTestCore) refreshForm(value string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "client_id": {h.client.ID}, "refresh_token": {value}}
}
func (h *oauthTestCore) issue(t *testing.T, role Role, approved time.Time) (string, fosite.AccessResponder) {
	t.Helper()
	code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), role, approved)
	require.NoError(t, err)
	r, err := h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.NoError(t, err)
	require.Equal(t, oauthScope, r.GetExtra("scope"))
	require.NotEmpty(t, r.GetExtra("refresh_token"))
	return code, r
}
func testRefresh(r fosite.AccessResponder) string { return r.GetExtra("refresh_token").(string) }
func (h *oauthTestCore) restart(t *testing.T) *oauthTestCore {
	t.Helper()
	require.NoError(t, h.store.Close())
	s, err := OpenStore(h.store.path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return &oauthTestCore{store: s, provider: s.oauth, client: h.client}
}

func TestOAuthProviderActualFactoriesDeadlineHydrationSanitizationRestart(t *testing.T) {
	for _, left := range []time.Duration{45 * time.Second, 9 * time.Minute, oauthGrantLifetime} {
		t.Run(left.String(), func(t *testing.T) {
			h := newOAuthTestCore(t)
			approved := time.Now().UTC().Add(left - oauthGrantLifetime)
			code, first := h.issue(t, Developer, approved)
			require.LessOrEqual(t, first.GetExtra("expires_in").(int64), int64((10 * time.Minute).Seconds()))
			before, err := h.provider.inspect(context.Background(), first.GetAccessToken())
			require.NoError(t, err)
			deadline := approved.Add(oauthGrantLifetime)
			for _, kind := range []fosite.TokenType{fosite.AccessToken, fosite.RefreshToken} {
				require.False(t, before.GetSession().GetExpiresAt(kind).After(deadline))
			}
			require.WithinDuration(t, deadline, before.GetSession().GetExpiresAt(fosite.RefreshToken), time.Second)
			before.GetSession().SetExpiresAt(fosite.AccessToken, time.Time{}) // detached hydration
			h = h.restart(t)
			rotated, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
			require.NoError(t, err)
			require.NotEqual(t, testRefresh(first), testRefresh(rotated))
			after, err := h.provider.inspect(context.Background(), rotated.GetAccessToken())
			require.NoError(t, err)
			require.Equal(t, before.GetID(), after.GetID())
			require.WithinDuration(t, deadline, after.GetSession().GetExpiresAt(fosite.RefreshToken), time.Second)
			require.False(t, after.GetSession().GetExpiresAt(fosite.AccessToken).After(deadline))
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.Error(t, err)
			b, err := os.ReadFile(h.store.path + "-wal")
			require.NoError(t, err)
			for _, raw := range []string{code, first.GetAccessToken(), rotated.GetAccessToken(), testRefresh(first), testRefresh(rotated), testOAuthVerifier, "code_verifier"} {
				require.NotContains(t, string(b), raw)
			}
			h = h.restart(t)
			_, err = h.provider.inspect(context.Background(), rotated.GetAccessToken())
			require.NoError(t, err)
		})
	}
}

func TestOAuthProviderRejectsUnapprovedAuthorityAndExactRedirectSubstitution(t *testing.T) {
	mutations := map[string]func(url.Values){
		"plain":             func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"noPKCE":            func(v url.Values) { v.Del("code_challenge") },
		"noMethod":          func(v url.Values) { v.Del("code_challenge_method") },
		"callbackPath":      func(v url.Values) { v.Set("redirect_uri", testOAuthCallback+"/evil") },
		"callbackHost":      func(v url.Values) { v.Set("redirect_uri", "http://localhost:32123/callback") },
		"resourceEmpty":     func(v url.Values) { v.Set("resource", "") },
		"resourceMissing":   func(v url.Values) { v.Del("resource") },
		"resourceAlias":     func(v url.Values) { v.Set("resource", "http://localhost:19380/mcp") },
		"resourceRepeat":    func(v url.Values) { v.Add("resource", canonicalResource(DefaultPort)) },
		"audienceInjection": func(v url.Values) { v.Set("audience", canonicalResource(DefaultPort)) },
		"role":              func(v url.Values) { v.Set("role", "developer") },
		"scope":             func(v url.Values) { v.Set("scope", "mcp developer") },
		"implicit":          func(v url.Values) { v.Set("response_type", "token") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			h := newOAuthTestCore(t)
			form := h.authorizeForm()
			mutate(form)
			code, err := h.provider.mintApprovedCode(context.Background(), form, Observer, time.Now().UTC())
			require.Error(t, err)
			require.Empty(t, code)
			require.Empty(t, testSQLState(t, h.store).OAuth.Grants)
			require.Empty(t, testSQLState(t, h.store).OAuth.Codes)
		})
	}
}

func TestOAuthProviderExchangeOuterRollbackIncludesPKCEDeletion(t *testing.T) {
	h := newOAuthTestCore(t)
	code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
	require.NoError(t, err)
	write := h.store.beforeCommit
	h.store.beforeCommit = func() error { return errors.New("disk full") }
	out, err := h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.Error(t, err)
	require.Nil(t, out)
	require.Len(t, testSQLState(t, h.store).OAuth.PKCE, 1)
	for _, row := range testSQLState(t, h.store).OAuth.Codes {
		require.True(t, row.Active)
	}
	h.store.beforeCommit = write
	out, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.NoError(t, err)
	require.NotEmpty(t, out.GetAccessToken())
	// An actual handler error after reading a code also leaves its state intact.
	code, err = h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
	require.NoError(t, err)
	bad := h.exchangeForm(code)
	bad.Set("code_verifier", "incorrect-verifier-that-is-long-enough-00000000000")
	_, err = h.provider.exchange(context.Background(), bad)
	require.Error(t, err)
	_, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.NoError(t, err)
}

func TestOAuthProviderRefreshResourcePresenceAndRealDefaultTokenSource(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	for _, resources := range [][]string{{""}, {"", canonicalResource(DefaultPort)}, {canonicalResource(DefaultPort), ""}, {canonicalResource(DefaultPort), canonicalResource(DefaultPort)}, {"http://localhost:19380/mcp"}} {
		form := h.refreshForm(testRefresh(first))
		form["resource"] = resources
		_, err := h.provider.exchange(context.Background(), form)
		require.Error(t, err)
	}
	seenResource := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "invalid form", 400)
			return
		}
		_, seenResource = r.PostForm["resource"]
		out, err := h.provider.exchange(r.Context(), r.PostForm)
		if err != nil {
			t.Error(err)
			http.Error(w, "refused", 400)
			return
		}
		h.provider.provider.WriteAccessResponse(r.Context(), w, nil, out)
	}))
	defer server.Close()
	cfg := oauth2.Config{ClientID: h.client.ID, Endpoint: oauth2.Endpoint{TokenURL: server.URL, AuthStyle: oauth2.AuthStyleInParams}}
	token, err := cfg.TokenSource(context.Background(), &oauth2.Token{AccessToken: "expired", RefreshToken: testRefresh(first), Expiry: time.Now().Add(-time.Hour)}).Token()
	require.NoError(t, err)
	require.False(t, seenResource)
	explicit := h.refreshForm(token.RefreshToken)
	explicit.Set("resource", canonicalResource(DefaultPort))
	_, err = h.provider.exchange(context.Background(), explicit)
	require.NoError(t, err)
}

func TestOAuthProviderConcurrentCodeAndRefreshReplayRevokesFamily(t *testing.T) {
	for _, kind := range []string{"code", "refresh"} {
		t.Run(kind, func(t *testing.T) {
			h := newOAuthTestCore(t)
			code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
			require.NoError(t, err)
			form := h.exchangeForm(code)
			if kind == "refresh" {
				first, err := h.provider.exchange(context.Background(), form)
				require.NoError(t, err)
				form = h.refreshForm(testRefresh(first))
			}
			var wg sync.WaitGroup
			results := make(chan fosite.AccessResponder, 2)
			for range 2 {
				wg.Go(func() {
					out, err := h.provider.exchange(context.Background(), form)
					if err == nil {
						results <- out
					}
				})
			}
			wg.Wait()
			close(results)
			require.Len(t, results, 1)
			for out := range results {
				_, err := h.provider.inspect(context.Background(), out.GetAccessToken())
				require.Error(t, err)
				h = h.restart(t)
				_, err = h.provider.inspect(context.Background(), out.GetAccessToken())
				require.Error(t, err)
			}
		})
	}
}

func TestOAuthProviderWrongClientOrTamperedCredentialCannotPoisonVictim(t *testing.T) {
	for _, kind := range []string{"code", "refresh", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			h := newOAuthTestCore(t)
			code, first := h.issue(t, Observer, time.Now().UTC())
			other, err := h.provider.registerClient(context.Background(), "other", []string{testOAuthCallback})
			require.NoError(t, err)
			var form url.Values
			switch kind {
			case "code":
				form = h.exchangeForm(code)
			case "refresh":
				form = h.refreshForm(testRefresh(first))
			default:
				form = url.Values{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}}
			}
			form.Set("client_id", other.ID)
			if kind == "revoke" {
				err = h.provider.revokeToken(context.Background(), form)
			} else {
				_, err = h.provider.exchange(context.Background(), form)
			}
			require.Error(t, err)
			form.Set("client_id", h.client.ID)
			key := "code"
			if kind == "refresh" {
				key = "refresh_token"
			}
			if kind == "revoke" {
				key = "token"
			}
			value := form.Get(key)
			parts := strings.Split(value, ".")
			require.Len(t, parts, 2)
			form.Set(key, "tampered."+parts[1])
			if kind == "revoke" {
				err = h.provider.revokeToken(context.Background(), form)
			} else {
				_, err = h.provider.exchange(context.Background(), form)
			}
			require.Error(t, err)
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.NoError(t, err)
			require.Empty(t, h.store.revocationDebt)
		})
	}
}

func TestOAuthProviderFailedRevokeAndReplayDebtCannotEscapeThroughWrites(t *testing.T) {
	for _, kind := range []string{"revoke-access", "revoke-refresh", "code-replay", "refresh-reuse"} {
		t.Run(kind, func(t *testing.T) {
			h := newOAuthTestCore(t)
			code, first := h.issue(t, Developer, time.Now().UTC())
			current := first
			if kind == "refresh-reuse" {
				var err error
				current, err = h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
				require.NoError(t, err)
			}
			principal, ok := h.store.authenticatePrincipal(current.GetAccessToken())
			require.True(t, ok)
			_, unrelated := h.issue(t, Observer, time.Now().UTC())
			write := h.store.beforeCommit
			writes := 0
			h.store.beforeCommit = func() error { writes++; return errors.New("refused") }
			var err error
			switch kind {
			case "code-replay":
				_, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
			case "refresh-reuse":
				_, err = h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
			default:
				value := first.GetAccessToken()
				if kind == "revoke-refresh" {
					value = testRefresh(first)
				}
				err = h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {value}})
			}
			require.Error(t, err)
			require.NotEmpty(t, h.store.RevocationWarning())
			require.Len(t, h.store.revocationDebt, 1)
			for range 5 {
				_, ok = h.store.authenticatePrincipal(current.GetAccessToken())
				require.False(t, ok)
				_, err = h.provider.exchange(context.Background(), h.refreshForm(testRefresh(current)))
				require.Error(t, err)
			}
			require.Equal(t, 1, writes, "denied authentication must not drive refused-disk retries")
			for _, value := range []string{current.GetAccessToken(), testRefresh(current)} {
				require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {value}}))
			}
			w := httptest.NewRecorder()
			h.provider.writeRevocation(w, errors.New("commit refused"))
			require.Equal(t, 503, w.Code)
			// A failed unrelated static write cannot remove RAM deny/debt.
			_, raw, err := h.store.Create("unrelated", Observer, nil)
			require.Error(t, err)
			require.Empty(t, raw)
			require.Len(t, h.store.revocationDebt, 1)
			h.store.mu.Lock()
			_, ok = h.store.availableOAuthGrant(principal.ID)
			h.store.mu.Unlock()
			require.False(t, ok)
			h.store.beforeCommit = write
			_, _, err = h.store.Create("successful unrelated commit", Observer, nil)
			require.NoError(t, err)
			require.Empty(t, h.store.revocationDebt)
			require.Empty(t, h.store.RevocationWarning())
			h = h.restart(t)
			_, err = h.provider.inspect(context.Background(), current.GetAccessToken())
			require.Error(t, err)
			_, err = h.provider.inspect(context.Background(), unrelated.GetAccessToken())
			require.NoError(t, err)
		})
	}
}

func TestOAuthProviderZeroSuccessfulRevocationWritesCrashBoundaryAndRetry(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	h.store.beforeCommit = func() error { return errors.New("disk offline") }
	require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}}))
	for range 10 {
		require.Error(t, h.store.retryOAuthRevocations(time.Now().Add(time.Hour)))
	}
	require.Equal(t, time.Minute, h.store.revocationRetryDelay)
	h = h.restart(t) // Zero successful revoke writes: crash cannot preserve RAM intent.
	_, err := h.provider.inspect(context.Background(), first.GetAccessToken())
	require.NoError(t, err)
	h.store.beforeCommit = func() error { return errors.New("disk offline") }
	require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}}))
	h.store.beforeCommit = nil
	require.NoError(t, h.store.retryOAuthRevocations(time.Now().Add(time.Hour)))
	h = h.restart(t)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}

type oauthFailingRefreshStorage struct {
	*oauthStorage
	called bool
}

func (s *oauthFailingRefreshStorage) CreateRefreshTokenSession(context.Context, string, string, fosite.Requester) error {
	s.called = true
	return errors.New("injected Fosite storage callback failure")
}
func TestOAuthProviderLibraryRollbackAfterPKCEAndAccessCreation(t *testing.T) {
	h := newOAuthTestCore(t)
	code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
	require.NoError(t, err)
	original := h.provider.provider
	failing := &oauthFailingRefreshStorage{oauthStorage: h.provider.storage}
	cfg := oauthConfig(testSQLState(t, h.store).OAuth.Key)
	h.provider.provider = compose.Compose(cfg, failing, h.provider.strategy, compose.OAuth2AuthorizeExplicitFactory, compose.OAuth2RefreshTokenGrantFactory, compose.OAuth2PKCEFactory, compose.OAuth2TokenRevocationFactory, compose.OAuth2TokenIntrospectionFactory)
	response, err := h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.Error(t, err)
	require.Nil(t, response)
	require.True(t, failing.called)
	require.Len(t, testSQLState(t, h.store).OAuth.PKCE, 1)
	require.Empty(t, testSQLState(t, h.store).OAuth.Access)
	require.Empty(t, testSQLState(t, h.store).OAuth.Refresh)
	for _, row := range testSQLState(t, h.store).OAuth.Codes {
		require.True(t, row.Active)
	}
	h.provider.provider = original
	_, err = h.provider.exchange(context.Background(), h.exchangeForm(code))
	require.NoError(t, err)
}

func TestOAuthProviderMultipleRevocationDebtsOverlayOneUnrelatedCommit(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	_, second := h.issue(t, Developer, time.Now().UTC())
	_, unrelated := h.issue(t, Observer, time.Now().UTC())
	write := h.store.beforeCommit
	h.store.beforeCommit = func() error { return errors.New("disk offline") }
	for _, token := range []fosite.AccessResponder{first, second} {
		require.Error(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {token.GetAccessToken()}}))
	}
	require.Len(t, h.store.revocationDebt, 2)
	h.store.beforeCommit = write
	_, _, err := h.store.Create("unrelated successful write", Observer, nil)
	require.NoError(t, err)
	require.Empty(t, h.store.revocationDebt)
	h = h.restart(t)
	for _, token := range []fosite.AccessResponder{first, second} {
		_, inspectErr := h.provider.inspect(context.Background(), token.GetAccessToken())
		require.Error(t, inspectErr)
	}
	_, err = h.provider.inspect(context.Background(), unrelated.GetAccessToken())
	require.NoError(t, err)
}
