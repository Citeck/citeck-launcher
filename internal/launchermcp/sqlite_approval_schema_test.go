package launchermcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApprovalSchemaUpgradePreservesVersionThreeCredentials(t *testing.T) {
	h := newOAuthTestCore(t)
	_, token := h.issue(t, Observer, time.Now().UTC())
	key := append([]byte(nil), h.store.key...)
	// Version 3 differs only by these owner-selected fields. The real Fosite
	// credentials above must survive upgrading, with their original deadline.
	for _, statement := range []string{
		"ALTER TABLE oauth_grants DROP COLUMN name",
		"ALTER TABLE oauth_pending DROP COLUMN name",
		"ALTER TABLE oauth_pending DROP COLUMN grant_deadline",
		"PRAGMA user_version=3",
	} {
		_, err := h.store.db.Exec(statement)
		require.NoError(t, err)
	}
	require.NoError(t, h.store.Close())
	reopened, err := OpenStore(h.store.path)
	require.NoError(t, err)
	defer reopened.Close()
	require.Equal(t, key, reopened.key)
	var version int
	require.NoError(t, reopened.db.QueryRow("PRAGMA user_version").Scan(&version))
	require.Equal(t, 4, version)
	rows, err := reopened.ListOAuthConnections(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].Name)
	require.False(t, rows[0].NeverExpires)
	require.WithinDuration(t, rows[0].ApprovedAt.Add(oauthGrantLifetime), rows[0].ExpiresAt, time.Millisecond)
	_, err = reopened.oauth.inspect(context.Background(), token.GetAccessToken())
	require.NoError(t, err)
	_, err = reopened.oauth.exchange(context.Background(), h.refreshForm(testRefresh(token)))
	require.NoError(t, err)
}
