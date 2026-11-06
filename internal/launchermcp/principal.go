package launchermcp

import (
	"context"
	"database/sql"
	"github.com/ory/fosite"
	"strings"
	"time"
)

type principalKind string

const (
	staticPrincipal principalKind = "static"
	oauthPrincipal  principalKind = "oauth"
)

// OAuth identity is the stable grant, not the rotating bearer or public client.
type authPrincipal struct {
	Kind      principalKind
	ID        string
	Role      Role
	ExpiresAt *time.Time
}

func (p authPrincipal) artifactIdentity() string {
	if p.Kind == oauthPrincipal {
		return "oauth:" + p.ID
	}
	return p.ID
}
func (s *Store) authenticatePrincipal(value string) (authPrincipal, bool) {
	if len(value) > 512 {
		return authPrincipal{}, false
	}
	if strings.HasPrefix(value, "citeck_mcp_") {
		t, ok := s.Authenticate(value)
		return authPrincipal{Kind: staticPrincipal, ID: t.ID, Role: t.Role, ExpiresAt: t.ExpiresAt}, ok
	}
	r, err := s.oauth.inspect(context.Background(), value)
	if err != nil {
		return authPrincipal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.availableOAuthGrant(r.GetID())
	if !ok {
		return authPrincipal{}, false
	}
	now := time.Now().UTC()
	key := "oauth:" + g.ID
	if s.lastUsedAttempt == nil {
		s.lastUsedAttempt = map[string]time.Time{}
	}
	if (g.LastUsedAt == nil || now.Sub(*g.LastUsedAt) >= time.Minute) && now.Sub(s.lastUsedAttempt[key]) >= time.Minute {
		s.lastUsedAttempt[key] = now
		_ = s.writeLocked(context.Background(), "", func(ctx context.Context) error {
			_, e := s.transaction(ctx).q.ExecContext(ctx, "UPDATE oauth_grants SET last_used=? WHERE id=? AND (last_used IS NULL OR last_used<=?)", now.UnixNano(), g.ID, now.Add(-time.Minute).UnixNano())
			if e != nil {
				return mcpStorageError(e)
			}
			_, e = s.transaction(ctx).q.ExecContext(ctx, "UPDATE oauth_clients SET expiry=max(expiry,?) WHERE id=?", now.Add(oauthGrantLifetime).UnixNano(), g.ClientID)
			return mcpStorageError(e)
		})
	}
	if s.uncertain {
		return authPrincipal{}, false
	}
	expires := r.GetSession().GetExpiresAt(fosite.AccessToken)
	return authPrincipal{Kind: oauthPrincipal, ID: g.ID, Role: g.Role, ExpiresAt: &expires}, true
}

// Caller holds the store mutex; only a point grant query is needed for artifacts.
func (s *Store) availableOAuthGrant(id string) (OAuthGrant, bool) {
	var g OAuthGrant
	var approved, deadline, epoch int64
	var revoked bool
	var last, revokedAt sql.NullInt64
	if s.closed || s.uncertain || !s.settingsCache.Enabled || debtContains(s.revocationDebt, id) {
		return g, false
	}
	err := s.db.QueryRow("SELECT id,client,role,resource,approved,deadline,epoch,revoked,revoked_at,last_used FROM oauth_grants WHERE id=?", id).Scan(&g.ID, &g.ClientID, &g.Role, &g.Resource, &approved, &deadline, &epoch, &revoked, &revokedAt, &last)
	g.ApprovedAt = time.Unix(0, approved).UTC()
	g.ExpiresAt = time.Unix(0, deadline).UTC()
	g.LastUsedAt = tokenTime(last)
	g.RevokedAt = tokenTime(revokedAt)
	var currentEpoch int64
	if err == nil {
		err = s.db.QueryRow("SELECT epoch FROM mcp_settings WHERE id=1").Scan(&currentEpoch)
	}
	return g, err == nil && !revoked && g.Role.Valid() && epoch == currentEpoch && g.Resource == canonicalResource(s.settingsCache.Port) && time.Now().Before(g.ExpiresAt)
}
