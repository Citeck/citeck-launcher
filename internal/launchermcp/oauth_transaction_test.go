package launchermcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOAuthRequestCancellationRollsBackWithoutClosingAuthority(t *testing.T) {
	for _, flow := range []string{"code", "refresh"} {
		t.Run(flow, func(t *testing.T) {
			h := newOAuthTestCore(t)
			_, existing := h.issue(t, Observer, time.Now().UTC())
			_, manual, err := h.store.Create("manual", Observer, nil)
			require.NoError(t, err)
			code, err := h.provider.mintApprovedCode(context.Background(), h.authorizeForm(), Observer, time.Now().UTC())
			require.NoError(t, err)
			form := h.exchangeForm(code)
			if flow == "refresh" {
				first, e := h.provider.exchange(context.Background(), form)
				require.NoError(t, e)
				form = h.refreshForm(testRefresh(first))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.store.beforeCommit = func() error { cancel(); return nil }
			out, err := h.provider.exchange(ctx, form)
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, out, "no credentials escape a canceled issuance")
			h.store.beforeCommit = nil
			require.False(t, h.store.uncertain)
			_, ok := h.store.Authenticate(manual)
			require.True(t, ok)
			_, err = h.provider.inspect(context.Background(), existing.GetAccessToken())
			require.NoError(t, err)
			out, err = h.provider.exchange(context.Background(), form)
			require.NoError(t, err, "code/PKCE or current refresh must survive rollback")
			require.NotEmpty(t, out.GetAccessToken())
		})
	}
}

func TestOAuthCancellationAfterBeginKeepsSQLLifetimeControlled(t *testing.T) {
	h := newOAuthTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := h.store.oauthWrite(ctx, func(ctx context.Context) error {
		u := h.store.transaction(ctx)
		cancel()
		// Query after cancellation must not interrupt SQLite or let database/sql
		// auto-rollback race the explicit outcome. The UOW still rolls back below.
		_, err := u.q.ExecContext(ctx, "UPDATE oauth_clients SET name='canceled' WHERE id=?", h.client.ID)
		require.NoError(t, err, "SQL must remain under the controlled lifetime after request cancellation")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, h.store.uncertain)
	var name string
	require.NoError(t, h.store.db.QueryRow("SELECT name FROM oauth_clients WHERE id=?", h.client.ID).Scan(&name))
	require.Equal(t, h.client.Name, name)
	_, err = h.provider.registerClient(context.Background(), "retry", []string{testOAuthCallback})
	require.NoError(t, err)
}

func TestOAuthCanceledOwnerRevocationRetainsDenyAndRetries(t *testing.T) {
	for _, phase := range []string{"before-begin", "before-commit"} {
		t.Run(phase, func(t *testing.T) {
			h := newOAuthTestCore(t)
			_, first := h.issue(t, Observer, time.Now().UTC())
			_, other := h.issue(t, Observer, time.Now().UTC())
			grant, err := h.provider.inspect(context.Background(), first.GetAccessToken())
			require.NoError(t, err)
			_, manual, err := h.store.Create("manual", Observer, nil)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "before-begin" {
				cancel()
			} else {
				h.store.beforeCommit = func() error { cancel(); return nil }
			}
			require.ErrorIs(t, h.store.RevokeOAuthConnection(ctx, grant.GetID()), context.Canceled)
			require.False(t, h.store.uncertain)
			require.Contains(t, h.store.revocationDebt, grant.GetID())
			require.Equal(t, oauthRevocationWarning, h.store.RevocationWarning())
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.Error(t, err)
			_, err = h.provider.inspect(context.Background(), other.GetAccessToken())
			require.NoError(t, err)
			_, ok := h.store.Authenticate(manual)
			require.True(t, ok)
			h.store.beforeCommit = nil
			require.NoError(t, h.store.retryOAuthRevocations(time.Now().Add(time.Minute)))
			require.Empty(t, h.store.revocationDebt)
			require.Empty(t, h.store.RevocationWarning())
			h = h.restart(t)
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.Error(t, err, "retry must persist denial across restart")
		})
	}
}

func TestOAuthDriverCommitFailureStillClosesAuthority(t *testing.T) {
	h := newOAuthTestCore(t)
	_, first := h.issue(t, Observer, time.Now().UTC())
	// A deferred constraint fails in the actual SQLite COMMIT, not in the
	// beforeCommit seam or statement. database/sql cannot certify rollback.
	_, err := h.store.db.Exec("CREATE TABLE test_commit_failure(client TEXT REFERENCES oauth_clients(id) DEFERRABLE INITIALLY DEFERRED)")
	require.NoError(t, err)
	err = h.store.oauthWrite(context.Background(), func(ctx context.Context) error {
		_, execErr := h.store.transaction(ctx).q.ExecContext(ctx, "INSERT INTO test_commit_failure VALUES('missing')")
		require.NoError(t, execErr)
		return nil
	})
	require.ErrorIs(t, err, errMCPUncertain)
	_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
	require.ErrorIs(t, err, errMCPUncertain)
	_, raw, err := h.store.Create("no credential", Observer, nil)
	require.ErrorIs(t, err, errMCPUncertain)
	require.Empty(t, raw)
}
