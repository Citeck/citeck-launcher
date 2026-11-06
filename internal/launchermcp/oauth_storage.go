package launchermcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/ory/fosite"
	core "github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/storage"
	"net/url"
	"time"
)

// Only an absent row is an unknown credential. Driver, cancellation and
// hydration failures must not be reported as RFC7009 success.
func oauthLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fosite.ErrNotFound
	}
	return mcpStorageError(err)
}

func debtContains(m map[string]time.Time, id string) bool { _, ok := m[id]; return ok }

type oauthStorage struct{ store *Store }

var _ storage.Transactional = (*oauthStorage)(nil)
var _ core.CoreStorage = (*oauthStorage)(nil)
var _ core.TokenRevocationStorage = (*oauthStorage)(nil)

func (a *oauthStorage) q(ctx context.Context) queryer { return a.store.transaction(ctx).q }
func (a *oauthStorage) BeginTX(ctx context.Context) (context.Context, error) {
	a.store.transaction(ctx)
	return ctx, nil
}
func (a *oauthStorage) Commit(ctx context.Context) error { a.store.transaction(ctx); return nil }
func (a *oauthStorage) Rollback(ctx context.Context) error {
	a.store.transaction(ctx).rolledBack = true
	return nil
}
func (a *oauthStorage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var encoded string
	e := a.q(ctx).QueryRowContext(ctx, "SELECT redirects FROM oauth_clients WHERE id=? AND epoch=?", id, a.store.transaction(ctx).settings.Epoch).Scan(&encoded)
	if e != nil {
		return nil, oauthLookupError(e)
	}
	redirects := []string{}
	if e := json.Unmarshal([]byte(encoded), &redirects); e != nil {
		return nil, mcpStorageError(e)
	}
	return &fosite.DefaultClient{ID: id, Public: true, RedirectURIs: redirects, ResponseTypes: []string{"code"}, GrantTypes: []string{"authorization_code", "refresh_token"}, Scopes: []string{"mcp"}, Audience: []string{canonicalResource(a.store.transaction(ctx).settings.Port)}}, nil
}
func (*oauthStorage) ClientAssertionJWTValid(context.Context, string) error {
	return fosite.ErrRequestNotSupported
}
func (*oauthStorage) SetClientAssertionJWT(context.Context, string, time.Time) error {
	return fosite.ErrRequestNotSupported
}
func (a *oauthStorage) grantAvailable(ctx context.Context, id string) error {
	var deadline, epoch int64
	var revoked bool
	var res string
	var role string
	e := a.q(ctx).QueryRowContext(ctx, "SELECT deadline,epoch,revoked,resource,role FROM oauth_grants WHERE id=?", id).Scan(&deadline, &epoch, &revoked, &res, &role)
	if e != nil {
		return oauthLookupError(e)
	}
	settings := a.store.transaction(ctx).settings
	if !settings.Enabled || debtContains(a.store.revocationDebt, id) || revoked || deadline <= time.Now().UnixNano() || epoch != settings.Epoch || res != canonicalResource(settings.Port) || (role != string(Observer) && role != string(Developer)) {
		return fosite.ErrInactiveToken
	}
	return nil
}
func (a *oauthStorage) hydrate(ctx context.Context, id string, expiry int64, kind fosite.TokenType, form url.Values, target fosite.Session) (fosite.Requester, error) {
	var client, res string
	var approved, deadline int64
	e := a.q(ctx).QueryRowContext(ctx, "SELECT client,resource,approved,deadline FROM oauth_grants WHERE id=?", id).Scan(&client, &res, &approved, &deadline)
	if e != nil {
		return nil, oauthLookupError(e)
	}
	if expiry <= 0 || deadline <= 0 || expiry > deadline {
		return nil, errors.New("invalid original token expiry")
	}
	c, e := a.GetClient(ctx, client)
	if e != nil {
		return nil, mcpStorageError(e)
	}
	se := &oauthSession{GrantID: id, Deadline: time.Unix(0, deadline), ExpiresAt: map[fosite.TokenType]time.Time{}}
	se.ExpiresAt[kind] = time.Unix(0, expiry)
	if kind == fosite.AccessToken {
		var refreshExpiry int64
		e := a.q(ctx).QueryRowContext(ctx, "SELECT expiry FROM oauth_refresh_current WHERE grant_id=?", id).Scan(&refreshExpiry)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, mcpStorageError(e)
		}
		if e == nil {
			se.ExpiresAt[fosite.RefreshToken] = time.Unix(0, refreshExpiry)
		}
	}
	if target != nil {
		t, ok := target.(*oauthSession)
		if !ok {
			return nil, errors.New("unexpected session")
		}
		if t.GrantID == "" {
			*t = *se.Clone().(*oauthSession)
		}
	}
	return &fosite.Request{ID: id, RequestedAt: time.Unix(0, approved), Client: c, RequestedScope: fosite.Arguments{"mcp"}, GrantedScope: fosite.Arguments{"mcp"}, RequestedAudience: fosite.Arguments{res}, GrantedAudience: fosite.Arguments{res}, Form: form, Session: se}, nil
}
func (a *oauthStorage) CreateAuthorizeCodeSession(ctx context.Context, sig string, r fosite.Requester) error {
	_, e := a.q(ctx).ExecContext(ctx, "INSERT INTO oauth_codes VALUES(?,?,?,?,?)", sig, r.GetID(), r.GetSession().GetExpiresAt(fosite.AuthorizeCode).UnixNano(), true, r.GetRequestForm().Get("redirect_uri"))
	return mcpStorageError(e)
}
func (a *oauthStorage) GetAuthorizeCodeSession(ctx context.Context, sig string, target fosite.Session) (fosite.Requester, error) {
	var id, redir string
	var exp int64
	var active bool
	if e := a.q(ctx).QueryRowContext(ctx, "SELECT grant_id,expiry,active,redirect FROM oauth_codes WHERE signature=?", sig).Scan(&id, &exp, &active, &redir); e != nil {
		return nil, oauthLookupError(e)
	}
	r, e := a.hydrate(ctx, id, exp, fosite.AuthorizeCode, url.Values{"redirect_uri": {redir}}, target)
	if e != nil {
		return nil, mcpStorageError(e)
	}
	if !active {
		return r, fosite.ErrInvalidatedAuthorizeCode
	}
	return r, nil
}
func (a *oauthStorage) InvalidateAuthorizeCodeSession(ctx context.Context, sig string) error {
	r, e := a.q(ctx).ExecContext(ctx, "UPDATE oauth_codes SET active=0 WHERE signature=? AND active=1", sig)
	if e != nil {
		return mcpStorageError(e)
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fosite.ErrInvalidatedAuthorizeCode
	}
	return nil
}
func (a *oauthStorage) CreatePKCERequestSession(ctx context.Context, sig string, r fosite.Requester) error {
	_, e := a.q(ctx).ExecContext(ctx, "INSERT INTO oauth_pkce VALUES(?,?,?,?,?)", sig, r.GetID(), r.GetSession().GetExpiresAt(fosite.AuthorizeCode).UnixNano(), r.GetRequestForm().Get("code_challenge"), r.GetRequestForm().Get("code_challenge_method"))
	return mcpStorageError(e)
}
func (a *oauthStorage) GetPKCERequestSession(ctx context.Context, sig string, t fosite.Session) (fosite.Requester, error) {
	var id, c, m string
	var exp int64
	e := a.q(ctx).QueryRowContext(ctx, "SELECT grant_id,expiry,challenge,method FROM oauth_pkce WHERE signature=?", sig).Scan(&id, &exp, &c, &m)
	if e != nil {
		return nil, oauthLookupError(e)
	}
	return a.hydrate(ctx, id, exp, fosite.AuthorizeCode, url.Values{"code_challenge": {c}, "code_challenge_method": {m}}, t)
}
func (a *oauthStorage) DeletePKCERequestSession(ctx context.Context, sig string) error {
	_, e := a.q(ctx).ExecContext(ctx, "DELETE FROM oauth_pkce WHERE signature=?", sig)
	return mcpStorageError(e)
}
func (a *oauthStorage) CreateAccessTokenSession(ctx context.Context, sig string, r fosite.Requester) error {
	_, e := a.q(ctx).ExecContext(ctx, "INSERT INTO oauth_access VALUES(?,?,?,1)", sig, r.GetID(), r.GetSession().GetExpiresAt(fosite.AccessToken).UnixNano())
	if e != nil {
		return mcpStorageError(e)
	}
	_, e = a.q(ctx).ExecContext(ctx, "UPDATE oauth_grants SET credential_issued_at=coalesce(credential_issued_at,?) WHERE id=?", time.Now().UnixNano(), r.GetID())
	return mcpStorageError(e)
}
func (a *oauthStorage) GetAccessTokenSession(ctx context.Context, sig string, t fosite.Session) (fosite.Requester, error) {
	return a.access(ctx, sig, t, true)
}
func (a *oauthStorage) access(ctx context.Context, sig string, t fosite.Session, check bool) (fosite.Requester, error) {
	var id string
	var exp int64
	var active bool
	e := a.q(ctx).QueryRowContext(ctx, "SELECT grant_id,expiry,active FROM oauth_access WHERE signature=?", sig).Scan(&id, &exp, &active)
	if e != nil {
		return nil, oauthLookupError(e)
	}
	r, e := a.hydrate(ctx, id, exp, fosite.AccessToken, url.Values{}, t)
	if e != nil {
		return nil, mcpStorageError(e)
	}
	if check {
		if e := a.grantAvailable(ctx, id); e != nil {
			return r, e
		}
	}
	if !active {
		return r, fosite.ErrInactiveToken
	}
	return r, nil
}
func (a *oauthStorage) DeleteAccessTokenSession(ctx context.Context, sig string) error {
	_, e := a.q(ctx).ExecContext(ctx, "UPDATE oauth_access SET active=0 WHERE signature=?", sig)
	return mcpStorageError(e)
}
func (a *oauthStorage) CreateRefreshTokenSession(ctx context.Context, sig, _ string, r fosite.Requester) error {
	_, e := a.q(ctx).ExecContext(ctx, "INSERT INTO oauth_refresh_current VALUES(?,?,?)", sig, r.GetID(), r.GetSession().GetExpiresAt(fosite.RefreshToken).UnixNano())
	return mcpStorageError(e)
}
func (a *oauthStorage) GetRefreshTokenSession(ctx context.Context, sig string, t fosite.Session) (fosite.Requester, error) {
	return a.refresh(ctx, sig, t, true)
}
func (a *oauthStorage) refresh(ctx context.Context, sig string, t fosite.Session, check bool) (fosite.Requester, error) {
	var id string
	var exp int64
	active := true
	e := a.q(ctx).QueryRowContext(ctx, "SELECT grant_id,expiry FROM oauth_refresh_current WHERE signature=?", sig).Scan(&id, &exp)
	if errors.Is(e, sql.ErrNoRows) {
		active = false
		e = a.q(ctx).QueryRowContext(ctx, "SELECT grant_id,expiry FROM oauth_refresh_used WHERE signature=?", sig).Scan(&id, &exp)
	}
	if e != nil {
		return nil, oauthLookupError(e)
	}
	r, e := a.hydrate(ctx, id, exp, fosite.RefreshToken, url.Values{}, t)
	if e != nil {
		return nil, mcpStorageError(e)
	}
	if check {
		if e := a.grantAvailable(ctx, id); e != nil {
			return r, e
		}
	}
	if !active {
		return r, fosite.ErrInactiveToken
	}
	return r, nil
}
func (a *oauthStorage) DeleteRefreshTokenSession(ctx context.Context, sig string) error {
	var n int
	if e := a.q(ctx).QueryRowContext(ctx, "SELECT count(*) FROM oauth_refresh_used WHERE signature=?", sig).Scan(&n); e != nil {
		return mcpStorageError(e)
	}
	if n == 1 {
		return nil
	} // authenticated replay must retain its compact evidence
	return a.moveUsed(ctx, sig)
}
func (a *oauthStorage) moveUsed(ctx context.Context, sig string) error {
	q := a.q(ctx)
	r, e := q.ExecContext(ctx, "INSERT INTO oauth_refresh_used SELECT signature,grant_id,expiry FROM oauth_refresh_current WHERE signature=?", sig)
	if e != nil {
		return mcpStorageError(e)
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fosite.ErrInactiveToken
	}
	_, e = q.ExecContext(ctx, "DELETE FROM oauth_refresh_current WHERE signature=?", sig)
	return mcpStorageError(e)
}
func (a *oauthStorage) RotateRefreshToken(ctx context.Context, id, sig string) error {
	if e := a.moveUsed(ctx, sig); e != nil {
		return mcpStorageError(e)
	}
	_, e := a.q(ctx).ExecContext(ctx, "UPDATE oauth_access SET active=0 WHERE grant_id=?", id)
	return mcpStorageError(e)
}
func (a *oauthStorage) revoke(ctx context.Context, id string) error {
	a.store.transaction(ctx).revoke[id] = time.Now().UTC()
	_, e := a.q(ctx).ExecContext(ctx, "UPDATE oauth_grants SET revoked=1 WHERE id=?", id)
	return mcpStorageError(e)
}
func (a *oauthStorage) RevokeAccessToken(ctx context.Context, id string) error {
	return a.revoke(ctx, id)
}
func (a *oauthStorage) RevokeRefreshToken(ctx context.Context, id string) error {
	return a.revoke(ctx, id)
}
