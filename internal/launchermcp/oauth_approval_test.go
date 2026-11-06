package launchermcp

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ory/fosite"
	"github.com/stretchr/testify/require"
)

func TestOAuthApprovalOwnerNameAndLifetime(t *testing.T) {
	for _, kind := range []string{"default", "short", "long", "never"} {
		t.Run(kind, func(t *testing.T) {
			h := newOAuthTestCore(t)
			service := New(h.store, &fakeBackend{})
			defer service.Close()
			b := startBrowserCeremony(t, h, service)
			opts := OAuthApproval{Name: "  Pi on Mac  "}
			expected := time.Now().Add(oauthGrantLifetime)
			switch kind {
			case "short":
				expected = time.Now().Add(time.Hour)
				opts.ExpiresAt = &expected
			case "long":
				expected = time.Now().Add(90 * 24 * time.Hour)
				opts.ExpiresAt = &expected
			case "never":
				opts.NeverExpires = true
			}
			require.NoError(t, h.store.ApproveOAuthPending(context.Background(), b.id, Developer, opts))
			require.NoError(t, h.store.oauthView(context.Background(), func(ctx context.Context) error {
				pending, err := h.store.browserPending(ctx, b.id, b.nonce)
				require.NoError(t, err)
				require.Equal(t, "Pi on Mac", pending.name)
				return nil
			}))
			callback, err := h.store.finishAuthorization(context.Background(), b.id, b.nonce, b.csrf)
			require.NoError(t, err)
			uri, err := url.Parse(callback)
			require.NoError(t, err)
			token, err := h.provider.exchange(context.Background(), h.exchangeForm(uri.Query().Get("code")))
			require.NoError(t, err)
			connections, err := h.store.ListOAuthConnections(context.Background())
			require.NoError(t, err)
			require.Len(t, connections, 1)
			c := connections[0]
			require.Equal(t, "Pi on Mac", c.Name)
			require.NotEqual(t, c.Name, c.ClientName, "owner label must not rewrite client metadata")
			require.Equal(t, kind == "never", c.NeverExpires)
			var deadline time.Time
			if kind == "never" {
				require.True(t, c.ExpiresAt.IsZero())
				deadline = time.Unix(0, oauthNoDeadline)
			} else {
				require.WithinDuration(t, expected, c.ExpiresAt, time.Second)
				deadline = c.ExpiresAt
			}
			require.NoError(t, h.store.oauthView(context.Background(), func(ctx context.Context) error {
				r, sessionErr := h.provider.storage.GetRefreshTokenSession(ctx, h.provider.strategy.RefreshTokenSignature(ctx, testRefresh(token)), nil)
				require.NoError(t, sessionErr)
				require.Equal(t, deadline.UnixNano(), r.GetSession().GetExpiresAt(fosite.RefreshToken).UnixNano())
				access, sessionErr := h.provider.storage.GetAccessTokenSession(ctx, h.provider.strategy.AccessTokenSignature(ctx, token.GetAccessToken()), nil)
				require.NoError(t, sessionErr)
				require.LessOrEqual(t, time.Until(access.GetSession().GetExpiresAt(fosite.AccessToken)), 10*time.Minute+time.Second) // Fosite rounds token expiry to seconds.
				return nil
			}))
			rotated, err := h.provider.exchange(context.Background(), h.refreshForm(testRefresh(token)))
			require.NoError(t, err)
			require.NotEqual(t, testRefresh(token), testRefresh(rotated))
			require.NoError(t, h.store.Close())
			reopened, err := OpenStore(h.store.path)
			require.NoError(t, err)
			defer reopened.Close()
			saved, err := reopened.ListOAuthConnections(context.Background())
			require.NoError(t, err)
			require.Len(t, saved, 1)
			require.Equal(t, c.Name, saved[0].Name)
			require.Equal(t, c.NeverExpires, saved[0].NeverExpires)
			require.Equal(t, c.ExpiresAt, saved[0].ExpiresAt)
			_, err = reopened.oauth.exchange(context.Background(), h.refreshForm(testRefresh(rotated)))
			require.NoError(t, err)
			require.NoError(t, reopened.RevokeOAuthConnection(context.Background(), c.ID))
			_, err = reopened.oauth.inspect(context.Background(), rotated.GetAccessToken())
			require.Error(t, err)
		})
	}
}

func TestOAuthApprovalRejectsInvalidOptionsWithoutDeciding(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	overflow := time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, opts := range []OAuthApproval{{Name: strings.Repeat("x", 101)}, {ExpiresAt: &past}, {ExpiresAt: &future, NeverExpires: true}, {ExpiresAt: &overflow}} {
		h := newOAuthTestCore(t)
		service := New(h.store, &fakeBackend{})
		b := startBrowserCeremony(t, h, service)
		require.ErrorIs(t, h.store.ApproveOAuthPending(context.Background(), b.id, Observer, opts), ErrOAuthApprovalInvalid)
		rows, err := h.store.ListOAuthPending(context.Background())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "pending", rows[0].Status)
		service.Close()
	}
}
