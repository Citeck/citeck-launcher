package launchermcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ory/fosite"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRevocationReadFailuresAreNotSuccess(t *testing.T) {
	for _, failure := range []string{"canceled", "refresh-lookup", "access-lookup", "hydration"} {
		t.Run(failure, func(t *testing.T) {
			h := newOAuthTestCore(t)
			_, first := h.issue(t, Observer, time.Now().UTC())
			ctx := context.Background()
			restore := func() {}
			switch failure {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "refresh-lookup", "access-lookup":
				table := "oauth_refresh_current"
				if failure == "access-lookup" {
					table = "oauth_access"
				}
				// A real SQL lookup failure independent of BeginTx: a TEMP
				// relation shadows only this credential read, not settings.
				_, err := h.store.db.Exec("CREATE TEMP TABLE " + table + "(signature TEXT)")
				require.NoError(t, err)
				restore = func() {
					_, err := h.store.db.Exec("DROP TABLE temp." + table)
					require.NoError(t, err)
				}
			case "hydration":
				_, err := h.store.db.Exec("UPDATE oauth_clients SET redirects='not-json' WHERE id=?", h.client.ID)
				require.NoError(t, err)
				restore = func() {
					b, err := json.Marshal(h.client.Redirects)
					require.NoError(t, err)
					_, err = h.store.db.Exec("UPDATE oauth_clients SET redirects=? WHERE id=?", string(b), h.client.ID)
					require.NoError(t, err)
				}
			}
			form := url.Values{"client_id": {h.client.ID}, "token": {first.GetAccessToken()}}
			err := h.provider.revokeToken(ctx, form)
			require.Error(t, err)
			if failure == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
			}
			response := httptest.NewRecorder()
			h.provider.writeRevocation(response, err)
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Empty(t, h.store.revocationDebt, "unreadable identity is not an authenticated revocation")
			require.Empty(t, h.store.RevocationWarning())
			restore()
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.NoError(t, err)
			require.NoError(t, h.provider.revokeToken(context.Background(), form))
			_, err = h.provider.inspect(context.Background(), first.GetAccessToken())
			require.Error(t, err)
			require.NoError(t, h.provider.revokeToken(context.Background(), url.Values{"client_id": {h.client.ID}, "token": {"unknown"}}))
		})
	}
}

func TestSQLitePortChangeInvalidatesIssuerWithoutDeletingGrants(t *testing.T) {
	h := newOAuthTestCore(t)
	old := make([]fosite.AccessResponder, 0, 2)
	for range 2 {
		_, first := h.issue(t, Observer, time.Now().UTC())
		old = append(old, first)
	}
	_, manual, err := h.store.Create("manual", Developer, nil)
	require.NoError(t, err)
	metadata := h.store.List()
	key := append([]byte(nil), h.store.key...)
	h.store.beforeCommit = func() error { return errors.New("settings write refused") }
	require.Error(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort + 1}))
	require.Equal(t, DefaultPort, h.store.Settings().Port)
	for _, token := range old {
		_, err = h.provider.inspect(context.Background(), token.GetAccessToken())
		require.NoError(t, err)
	}
	h.store.beforeCommit = nil
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort + 1}))
	require.Equal(t, 2, testCount(t, h.store, "oauth_grants"))
	require.Equal(t, 1, testCount(t, h.store, "oauth_clients"))
	h.client, err = h.provider.registerClient(context.Background(), "replacement", []string{testOAuthCallback})
	require.NoError(t, err)
	h.issue(t, Developer, time.Now().UTC())
	require.NoError(t, h.store.SetSettings(Settings{Enabled: true, Port: DefaultPort}))
	h = h.restart(t)
	require.Equal(t, metadata, h.store.List())
	require.Equal(t, key, h.store.key)
	_, ok := h.store.Authenticate(manual)
	require.True(t, ok)
	for _, token := range old {
		_, err = h.provider.inspect(context.Background(), token.GetAccessToken())
		require.Error(t, err)
		_, err = h.provider.exchange(context.Background(), h.refreshForm(testRefresh(token)))
		require.Error(t, err)
	}
}
