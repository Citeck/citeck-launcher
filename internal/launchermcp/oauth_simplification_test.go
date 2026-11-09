package launchermcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ory/fosite"
	"github.com/stretchr/testify/require"
)

func TestSQLiteVersionUpgradePreservesActualCredentials(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			h := newOAuthTestCore(t)
			_, first := h.issue(t, Developer, time.Now().UTC())
			_, oldRevoked := h.issue(t, Observer, time.Now().UTC())
			revokedPrincipal, err := h.provider.inspect(context.Background(), oldRevoked.GetAccessToken())
			require.NoError(t, err)
			original := time.Now().Add(time.Hour).Truncate(time.Second)
			sig := h.provider.strategy.RefreshTokenSignature(context.Background(), testRefresh(first))
			_, err = h.store.db.Exec("UPDATE oauth_refresh_current SET expiry=? WHERE signature=?", original.UnixNano(), sig)
			require.NoError(t, err)
			rotated, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
			require.NoError(t, err)
			_, manual, err := h.store.Create("permanent", Observer, nil)
			require.NoError(t, err)
			// A tiny fixture of the actual old SQL schema, populated with real Fosite
			// credentials. No JSON OAuth reconstruction or synthetic history saturation.
			dir := filepath.Join(t.TempDir(), "private")
			require.NoError(t, os.Mkdir(dir, 0o700))
			path := filepath.Join(dir, "mcp.db")
			// SQLite's default file mode follows umask. Pre-create this old-schema
			// fixture privately, just as production creates its database file.
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			db, err := sql.Open("sqlite", sqliteDSN(path))
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			ddl, err := os.ReadFile("testdata/sqlite-v2.sql")
			require.NoError(t, err)
			if version == 1 {
				ddl = []byte(strings.Split(string(ddl), "CREATE TABLE oauth_pending")[0])
			}
			_, err = db.Exec(string(ddl))
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO mcp_meta(id,identity,revision,receipt,fingerprint) VALUES(1,?,4,'unused',?)", strings.Repeat("a", 64), strings.Repeat("b", 64))
			require.NoError(t, err)
			tables := []string{"mcp_root", "mcp_settings", "manual_tokens", "oauth_clients", "oauth_grants", "oauth_codes", "oauth_pkce", "oauth_access", "oauth_refresh_current", "oauth_refresh_used"}
			for _, table := range tables {
				columns := "*"
				if table == "oauth_grants" {
					columns = "id,client,resource,role,approved,deadline,epoch,revoked,revoked_at,last_used,credential_issued_at"
				}
				rows, e := h.store.db.Query("SELECT " + columns + " FROM " + table)
				require.NoError(t, e)
				cols, e := rows.Columns()
				require.NoError(t, e)
				for rows.Next() {
					values := make([]any, len(cols))
					scan := make([]any, len(cols))
					for i := range values {
						scan[i] = &values[i]
					}
					require.NoError(t, rows.Scan(scan...))
					targetCols := append([]string(nil), cols...)
					if table == "oauth_clients" {
						targetCols = append(targetCols, "redirect", "metadata")
						values = append(values, testOAuthCallback, []byte{})
					}
					if table == "oauth_grants" {
						targetCols = append(targetCols, "lifecycle", "reserved", "pending_expiry")
						values = append(values, "issued", 1, 0)
					}
					query := "INSERT INTO " + table + "(" + strings.Join(targetCols, ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")"
					_, e = db.Exec(query, values...)
					require.NoError(t, e)
				}
				require.NoError(t, rows.Err())
				require.NoError(t, rows.Close())
			}
			// Old cleanup made terminal grants authoritative without inventing a timestamp.
			_, err = db.Exec("UPDATE oauth_grants SET lifecycle='terminal',revoked=1,revoked_at=NULL WHERE id=?", revokedPrincipal.GetID())
			require.NoError(t, err)
			require.NoError(t, db.Close())
			s, err := OpenStore(path)
			require.NoError(t, err)
			defer s.Close()
			require.Equal(t, h.store.key, s.key)
			_, err = s.oauth.inspect(context.Background(), oldRevoked.GetAccessToken())
			require.Error(t, err)
			connections, err := s.ListOAuthConnections(context.Background())
			require.NoError(t, err)
			foundRevoked := false
			for _, c := range connections {
				if c.ID == revokedPrincipal.GetID() {
					foundRevoked = true
					require.True(t, c.Revoked)
					require.Nil(t, c.RevokedAt, "migration must not invent historical timestamps")
					require.True(t, c.ExpiresAt.After(time.Now()))
				}
			}
			require.True(t, foundRevoked)
			token, ok := s.Authenticate(manual)
			require.True(t, ok)
			require.Nil(t, token.ExpiresAt)
			require.NoError(t, s.oauthView(context.Background(), func(ctx context.Context) error {
				r, e := s.oauth.storage.GetRefreshTokenSession(ctx, sig, nil)
				require.ErrorIs(t, e, fosite.ErrInactiveToken)
				require.Equal(t, original, r.GetSession().GetExpiresAt(fosite.RefreshToken))
				return nil
			}))
			_, err = s.oauth.inspect(context.Background(), rotated.GetAccessToken())
			require.NoError(t, err)
			_, err = s.oauth.exchange(context.Background(), h.refreshForm(testRefresh(first)))
			require.Error(t, err)
			_, err = s.oauth.inspect(context.Background(), rotated.GetAccessToken())
			require.Error(t, err)
		})
	}
}

func TestUnsupportedExperimentalJSONIsNotModified(t *testing.T) {
	dir := testPrivateDir(t)
	raw := []byte(`{"version":2,"settings":{"port":19380},"oauth":{"key":"unreadable"}}`)
	path := filepath.Join(dir, "state.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	_, err := OpenStore(path)
	require.ErrorContains(t, err, "experimental OAuth JSON")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, after)
	_, err = os.Stat(filepath.Join(dir, "mcp.db"))
	require.True(t, os.IsNotExist(err))
}

func TestOAuthIdleMaintenanceDoesNotCommit(t *testing.T) {
	h := newOAuthTestCore(t)
	var writes atomic.Int32
	h.store.beforeCommit = func() error { writes.Add(1); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.store.runOAuthRevocationRetry(ctx) }()
	require.NoError(t, h.store.maintainOAuth(ctx))
	time.Sleep(1100 * time.Millisecond)
	cancel()
	<-done
	require.Zero(t, writes.Load())
}

func TestOAuthCloseDrainsRequestNeedingLocale(t *testing.T) {
	h := newOAuthTestCore(t)
	service := New(h.store, &fakeBackend{})
	service.SetOAuthLocale("ru")
	// Admit a request through the same drain accounting as Handler, then stop
	// it immediately before its locale read. Close cancels requests while it
	// owns service.mu: a locale reader using that mutex deterministically wedges
	// both sides, not just whichever goroutine happens to win a scheduling race.
	service.requestMu.Lock()
	service.requests.Add(1)
	service.requestMu.Unlock()
	locale := make(chan string, 1)
	go func() {
		defer service.requests.Done()
		<-service.requestContext.Done()
		locale <- service.oauthTranslator(httptest.NewRequest(http.MethodGet, canonicalIssuer(DefaultPort)+"/authorize", http.NoBody)).Locale()
	}()
	closeDone := make(chan struct{})
	go func() { service.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked with the admitted request's locale read")
	}
	require.Equal(t, "ru", <-locale)
}

func TestOAuthNativePortAndExtensionsBindActualExchange(t *testing.T) {
	for _, callback := range []string{"http://127.0.0.1:42124/callback", "http://[::1]:42124/callback"} {
		t.Run(callback, func(t *testing.T) {
			h := newOAuthTestCore(t)
			if strings.Contains(callback, "[::1]") {
				var err error
				h.client, err = h.provider.registerClient(context.Background(), "IPv6", []string{"http://[::1]:32123/callback"})
				require.NoError(t, err)
			}
			form := h.authorizeForm()
			form.Set("redirect_uri", callback)
			form["sdk_extension"] = []string{"ignored", "also ignored"}
			code, err := h.provider.mintApprovedCode(context.Background(), form, Observer, time.Now().UTC())
			require.NoError(t, err)
			wrong := h.exchangeForm(code)
			require.NotEqual(t, callback, wrong.Get("redirect_uri"))
			_, err = h.provider.exchange(context.Background(), wrong)
			require.Error(t, err)
			right := h.exchangeForm(code)
			right.Set("redirect_uri", callback)
			right.Set("sdk_extension", "ignored")
			token, err := h.provider.exchange(context.Background(), right)
			require.NoError(t, err)
			_, err = h.provider.inspect(context.Background(), token.GetAccessToken())
			require.NoError(t, err)
			require.NoError(t, h.store.Close())
			s, err := OpenStore(h.store.path)
			require.NoError(t, err)
			defer s.Close()
			_, err = s.oauth.inspect(context.Background(), token.GetAccessToken())
			require.NoError(t, err)
		})
	}
}

func TestOAuthDormantRegisteredClientCanSignInAgain(t *testing.T) {
	h := newOAuthTestCore(t)
	_, err := h.store.db.Exec("UPDATE oauth_clients SET created=?,expiry=?", time.Now().Add(-60*24*time.Hour).UnixNano(), time.Now().Add(-time.Hour).UnixNano())
	require.NoError(t, err)
	// The cached id remains valid until actual unused-registration cleanup.
	_, _, _, err = h.store.beginAuthorization(context.Background(), h.authorizeForm())
	require.NoError(t, err)
	metadata, err := h.store.ListOAuthPending(context.Background())
	require.NoError(t, err)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "nonce")
}
