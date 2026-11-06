package launchermcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/ory/fosite"
	"github.com/stretchr/testify/require"
	"net/url"
	"testing"
	"time"
)

func testCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
	return n
}

// Test-only legacy fixture builder; production never hydrates entire histories.
func testSQLState(t *testing.T, s *Store) testState {
	t.Helper()
	o := &oauthState{Key: append([]byte(nil), s.key...), Clients: map[string]oauthClient{}, Grants: map[string]OAuthGrant{}, Codes: map[string]oauthRecord{}, PKCE: map[string]oauthRecord{}, Access: map[string]oauthRecord{}, Refresh: map[string]oauthRecord{}}
	v := testState{Settings: s.Settings(), OAuth: o}
	for _, token := range s.List() {
		var h []byte
		require.NoError(t, s.db.QueryRow("SELECT hash FROM manual_tokens WHERE id=?", token.ID).Scan(&h))
		v.Tokens = append(v.Tokens, tokenRecord{Token: token, Hash: hex.EncodeToString(h)})
	}
	rows, err := s.db.Query("SELECT id,name,redirects,created,expiry FROM oauth_clients")
	require.NoError(t, err)
	for rows.Next() {
		var c oauthClient
		var red string
		var created, expiry int64
		require.NoError(t, rows.Scan(&c.ID, &c.Name, &red, &created, &expiry))
		require.NoError(t, json.Unmarshal([]byte(red), &c.Redirects))
		c.CreatedAt = time.Unix(0, created)
		c.ExpiresAt = time.Unix(0, expiry)
		o.Clients[c.ID] = c
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	rows, err = s.db.Query("SELECT id FROM oauth_grants")
	require.NoError(t, err)
	ids := []string{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	s.mu.Lock()
	for _, id := range ids {
		g, _ := s.availableOAuthGrant(id)
		o.Grants[id] = g
	}
	s.mu.Unlock()
	// availableOAuthGrant denies disabled/debt but still hydrates otherwise; use direct fields for fixture assertions.
	for _, id := range ids {
		g := o.Grants[id]
		if g.ID == "" {
			g.ID = id
			var a, d int64
			require.NoError(t, s.db.QueryRow("SELECT client,role,resource,approved,deadline FROM oauth_grants WHERE id=?", id).Scan(&g.ClientID, &g.Role, &g.Resource, &a, &d))
			g.ApprovedAt = time.Unix(0, a)
			g.ExpiresAt = time.Unix(0, d)
			o.Grants[id] = g
		}
	}
	for _, table := range []string{"oauth_codes", "oauth_pkce", "oauth_access", "oauth_refresh_current", "oauth_refresh_used"} {
		rows, err = s.db.Query("SELECT signature FROM " + table)
		require.NoError(t, err)
		sigs := []string{}
		for rows.Next() {
			var sig string
			require.NoError(t, rows.Scan(&sig))
			sigs = append(sigs, sig)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		for _, sig := range sigs {
			var r fosite.Requester
			var e error
			require.NoError(t, s.oauthView(context.Background(), func(ctx context.Context) error {
				switch table {
				case "oauth_codes":
					r, e = s.oauth.storage.GetAuthorizeCodeSession(ctx, sig, nil)
				case "oauth_pkce":
					r, e = s.oauth.storage.GetPKCERequestSession(ctx, sig, nil)
				case "oauth_access":
					r, e = s.oauth.storage.access(ctx, sig, nil, false)
				default:
					r, e = s.oauth.storage.refresh(ctx, sig, nil, false)
				}
				return nil
			}))
			require.NotNil(t, r)
			session := r.GetSession().Clone().(*oauthSession)
			row := oauthRecord{Active: e == nil, Request: oauthStoredRequest{ID: r.GetID(), ClientID: r.GetClient().GetID(), At: time.Now().UTC(), Requested: r.GetRequestedScopes(), Granted: r.GetGrantedScopes(), RequestedAudience: r.GetRequestedAudience(), GrantedAudience: r.GetGrantedAudience(), Form: r.GetRequestForm(), Session: session}}
			switch table {
			case "oauth_codes":
				o.Codes[sig] = row
			case "oauth_pkce":
				o.PKCE[sig] = row
			case "oauth_access":
				o.Access[sig] = row
			default:
				o.Refresh[sig] = row
			}
		}
	}
	return v
}

// Legacy JSON authority types exist only for validated, one-way SQL migration.
// Normal OAuth callbacks use SQL rows, never these maps.
type oauthState struct {
	Key     []byte                 `json:"key"`
	Clients map[string]oauthClient `json:"clients"`
	Grants  map[string]OAuthGrant  `json:"grants"`
	Codes   map[string]oauthRecord `json:"codes"`
	PKCE    map[string]oauthRecord `json:"pkce"`
	Access  map[string]oauthRecord `json:"access"`
	Refresh map[string]oauthRecord `json:"refresh"`
}

type oauthRecord struct {
	Active  bool               `json:"active"`
	Request oauthStoredRequest `json:"request"`
}

type oauthStoredRequest struct {
	ID                string           `json:"id"`
	ClientID          string           `json:"clientId"`
	At                time.Time        `json:"at"`
	Requested         fosite.Arguments `json:"requested"`
	Granted           fosite.Arguments `json:"granted"`
	RequestedAudience fosite.Arguments `json:"requestedAudience"`
	GrantedAudience   fosite.Arguments `json:"grantedAudience"`
	Form              url.Values       `json:"form,omitempty"`
	Session           *oauthSession    `json:"session"`
}

type testState struct {
	Settings Settings
	Tokens   []tokenRecord
	OAuth    *oauthState
}
