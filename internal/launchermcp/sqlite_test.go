//go:build !windows

package launchermcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	legacyreader "github.com/citeck/citeck-launcher/internal/launchermcp/testdata/legacyreader"
	"github.com/stretchr/testify/require"
)

func TestSQLiteManualMigrationAndActualOldReaderFence(t *testing.T) {
	path := t.TempDir() + "/state.json"
	old, err := legacyreader.OpenStore(path)
	require.NoError(t, err)
	token, raw, err := old.Create("permanent", legacyreader.Developer, nil)
	require.NoError(t, err)
	unused, _, err := old.Create("revoked", legacyreader.Observer, nil)
	require.NoError(t, err)
	require.NoError(t, old.Revoke(unused.ID))
	_, ok := old.Authenticate(raw)
	require.True(t, ok)
	require.NoError(t, old.SetSettings(legacyreader.Settings{Port: DefaultPort + 3, Enabled: true}))
	require.Len(t, old.List(), 2)
	require.True(t, old.Settings().Enabled)
	s, err := OpenStore(path)
	require.NoError(t, err)
	got, ok := s.Authenticate(raw)
	require.True(t, ok)
	require.Equal(t, token.ID, got.ID)
	require.Nil(t, got.ExpiresAt)
	require.NotNil(t, got.LastUsedAt)
	require.Equal(t, Developer, got.Role)
	require.Equal(t, DefaultPort+3, s.Settings().Port)
	require.NoError(t, s.Revoke(token.ID))
	key := append([]byte(nil), s.key...)
	require.NoError(t, s.Close())
	_, err = legacyreader.OpenStore(path)
	require.ErrorContains(t, err, "invalid MCP port")
	s, err = OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	require.Equal(t, key, s.key)
	_, ok = s.Authenticate(raw)
	require.False(t, ok, "SQL revocation must never fall back to the legacy backup")
	require.FileExists(t, filepath.Join(s.dir, "legacy.backup"))
}

func TestSQLiteOwnerLockPrivateSidecarsAndReconnectPolicy(t *testing.T) {
	// A trusted ancestor alias is canonicalized; the private leaf is not a link.
	base := t.TempDir()
	actualDir := filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(actualDir, 0o700))
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(actualDir, alias))
	path := filepath.Join(alias, "private?dsn#", "state.json")
	s, err := OpenStore(path)
	require.NoError(t, err)
	_, err = OpenStore(path)
	require.ErrorContains(t, err, "already owned")
	require.NoError(t, checkDBFiles(s.dir))
	s.db.SetMaxIdleConns(0)
	s.db.SetMaxIdleConns(1)
	require.NoError(t, verifyPragmas(s.db))
	key := append([]byte(nil), s.key...)
	require.NoError(t, s.Close())
	s, err = OpenStore(path)
	require.NoError(t, err)
	require.Equal(t, key, s.key)
	require.NoError(t, s.Close())
	require.NoError(t, os.Remove(s.path))
	_, err = OpenStore(path)
	require.ErrorContains(t, err, "missing after migration")
}
func TestSQLiteSchemaAndRootCorruptionNeverResetAuthority(t *testing.T) {
	for _, corrupt := range []string{"PRAGMA user_version=999", "DROP TABLE oauth_access", "PRAGMA ignore_check_constraints=ON; UPDATE mcp_root SET key=X'01'"} {
		t.Run(corrupt, func(t *testing.T) {
			s, err := OpenStore(t.TempDir() + "/state.json")
			require.NoError(t, err)
			path := s.path
			_, err = s.db.Exec(corrupt)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			_, err = OpenStore(path)
			require.Error(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestSQLiteKnownRevokeBeginFailureDeniesAndRetries(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	// Real SQLite read-only refusal: lookup succeeds, the immediate write UOW fails.
	_, err := h.store.db.Exec("PRAGMA query_only=ON")
	require.NoError(t, err)
	err = h.provider.revokeToken(context.Background(), map[string][]string{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}})
	require.Error(t, err)
	require.Len(t, h.store.revocationDebt, 1)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
	_, err = h.store.db.Exec("PRAGMA query_only=OFF")
	require.NoError(t, err)
	require.NoError(t, h.store.retryOAuthRevocations(time.Now().Add(time.Hour)))
	h = h.restart(t)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}
func TestSQLiteConcurrentAuthSettingsAndRevocation(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for range 5 {
				_, _ = h.store.authenticatePrincipal(first.GetAccessToken())
			}
		})
	}
	wg.Go(func() {
		_ = h.store.SetSettings(Settings{Port: DefaultPort})
		_ = h.store.SetSettings(Settings{Port: DefaultPort, Enabled: true})
	})
	wg.Go(func() {
		_ = h.provider.revokeToken(context.Background(), map[string][]string{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}})
	})
	wg.Wait()
	_, err := h.provider.inspect(context.Background(), first.GetAccessToken())
	require.Error(t, err)
}
func TestSQLiteFailedRollbackClosesMCP(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	err := h.store.oauthWrite(context.Background(), func(ctx context.Context) error {
		require.NoError(t, h.store.transaction(ctx).tx.Rollback())
		return errors.New("callback failed after transaction unexpectedly ended")
	})
	require.ErrorIs(t, err, errMCPUncertain)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.ErrorIs(t, err, errMCPUncertain)
	_, raw, err := h.store.Create("no credential", Observer, nil)
	require.ErrorIs(t, err, errMCPUncertain)
	require.Empty(t, raw)
}

func TestSQLiteActualAccessExpiryGCAndPointIndexes(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	_, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(first)))
	require.NoError(t, err)
	sig := h.provider.strategy.AccessTokenSignature(context.Background(), first.GetAccessToken())
	_, err = h.store.db.Exec("UPDATE oauth_access SET expiry=? WHERE signature=?", time.Now().Add(-time.Second).UnixNano(), sig)
	require.NoError(t, err)
	require.NoError(t, h.store.oauthWrite(context.Background(), h.store.collectOAuth))
	require.Equal(t, 1, testCount(t, h.store, "oauth_access"))
	require.Equal(t, 1, testCount(t, h.store, "oauth_refresh_used"))
	for _, query := range []string{"SELECT grant_id,expiry FROM oauth_refresh_used WHERE signature='x'", "SELECT role FROM manual_tokens WHERE hash=X'01'", "SELECT expiry FROM oauth_access WHERE expiry<1"} {
		rows, e := h.store.db.Query("EXPLAIN QUERY PLAN " + query)
		require.NoError(t, e)
		for rows.Next() {
			var id, parent, unused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
			require.NotContains(t, detail, "SCAN ", "point/expiry queries must use their indexes")
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
}

func TestSQLiteDurableRevokeCollectsOnlyRevokedCredentials(t *testing.T) {
	h := newOAuthTestCore(t)
	_, revoked := h.issue(t, Observer, time.Now().UTC())
	_, live := h.issue(t, Observer, time.Now().UTC())
	_, err := h.provider.inspect(context.Background(), revoked.GetAccessToken())
	require.NoError(t, err)
	require.NoError(t, h.provider.revokeToken(context.Background(), map[string][]string{"client_id": {h.client.ID}, "token": {revoked.GetAccessToken()}}))
	require.NoError(t, h.store.maintainOAuth(context.Background()))
	require.Equal(t, 1, testCount(t, h.store, "oauth_refresh_current"))
	require.Equal(t, 0, testCount(t, h.store, "oauth_refresh_used"))
	require.Equal(t, 2, testCount(t, h.store, "oauth_grants"))
	_, err = h.provider.inspect(context.Background(), live.GetAccessToken())
	require.NoError(t, err)
	h = h.restart(t)
	_, err = h.provider.inspect(context.Background(), revoked.GetAccessToken())
	require.Error(t, err)
}
