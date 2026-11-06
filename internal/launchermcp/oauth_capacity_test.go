package launchermcp

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// A SQL admission refusal after real Fosite consumption must roll back the
// whole issuance. A tiny injected trigger tests this without seeding histories.
func TestOAuthCapacityRefusalRollsBackRealCodePKCEAndRefreshConsumption(t *testing.T) {
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
			_, err = h.store.db.Exec("CREATE TEMP TRIGGER test_refuse BEFORE INSERT ON oauth_refresh_current BEGIN SELECT RAISE(ABORT,'history quota'); END")
			require.NoError(t, err)
			out, err := h.provider.exchange(context.Background(), form)
			require.Error(t, err)
			require.Nil(t, out)
			_, err = h.store.db.Exec("DROP TRIGGER test_refuse")
			require.NoError(t, err)
			out, err = h.provider.exchange(context.Background(), form)
			require.NoError(t, err)
			require.NotEmpty(t, out.GetAccessToken())
		})
	}
}
