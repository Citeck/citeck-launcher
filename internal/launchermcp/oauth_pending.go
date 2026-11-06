package launchermcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ory/fosite"
)

// ErrOAuthRequestUnavailable also covers expired or already-decided requests.
var ErrOAuthRequestUnavailable = errors.New("OAuth request is unavailable or already decided")

// OAuthPending is owner-visible metadata, not browser proof or a credential.
// ClientName is unverified text. Scope never selects a role.
type OAuthPending struct {
	ID                   string     `json:"id"`
	ClientID             string     `json:"clientId"`
	ClientName           string     `json:"clientName"`
	CorrelationCode      string     `json:"correlationCode"`
	Scope                string     `json:"scope"`
	Resource             string     `json:"resource"`
	Status               string     `json:"status"`
	Role                 Role       `json:"role,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	ExpiresAt            time.Time  `json:"expiresAt"`
	ApprovedAt           *time.Time `json:"approvedAt,omitempty"`
	GrantLifetimeSeconds int        `json:"grantLifetimeSeconds"`
}

type pendingAuthorization struct {
	OAuthPending
	form                url.Values
	nonceHash, csrfHash []byte
	epoch               int64
	name                string
	deadline            sql.NullInt64
}

const pendingColumns = "p.id,p.client,c.name,p.correlation,p.resource,p.status,p.role,p.created,p.expiry,p.approved,p.request,p.nonce_hash,p.csrf_hash,p.epoch,p.name,p.grant_deadline"

func scanPending(row interface{ Scan(...any) error }) (pendingAuthorization, error) {
	var p pendingAuthorization
	var role sql.NullString
	var approved sql.NullInt64
	var created, expiry int64
	var form string
	err := row.Scan(&p.ID, &p.ClientID, &p.ClientName, &p.CorrelationCode, &p.Resource, &p.Status, &role, &created, &expiry, &approved, &form, &p.nonceHash, &p.csrfHash, &p.epoch, &p.name, &p.deadline)
	if err != nil {
		return p, oauthLookupError(err)
	}
	p.Role = Role(role.String)
	p.CreatedAt = time.Unix(0, created).UTC()
	p.ExpiresAt = time.Unix(0, expiry).UTC()
	p.ApprovedAt = tokenTime(approved)
	p.Scope = oauthScope
	p.GrantLifetimeSeconds = int(oauthGrantLifetime.Seconds())
	if p.deadline.Valid && p.ApprovedAt != nil {
		p.GrantLifetimeSeconds = 0
		if p.deadline.Int64 != oauthNoDeadline {
			p.GrantLifetimeSeconds = int(time.Unix(0, p.deadline.Int64).Sub(*p.ApprovedAt).Seconds())
		}
	}
	if err = json.Unmarshal([]byte(form), &p.form); err != nil {
		return p, mcpStorageError(err)
	}
	if (p.Status == "pending" || p.Status == "approved") && !time.Now().Before(p.ExpiresAt) {
		p.Status = "expired"
	}
	return p, nil
}

func validateAuthorizationForm(input url.Values, resource string) error {
	form := cloneOAuthForm(input)
	if err := adaptOAuth(form, resource, false, true); err != nil {
		return err
	}
	if form.Get("response_type") != "code" || form.Get("code_challenge_method") != "S256" {
		return fosite.ErrInvalidRequest
	}
	challenge, err := base64.RawURLEncoding.DecodeString(form.Get("code_challenge"))
	if err != nil || len(challenge) != 32 || len(form.Get("code_challenge")) != 43 {
		return fosite.ErrInvalidRequest
	}
	return nil
}

func (s *Store) beginAuthorization(ctx context.Context, input url.Values) (pending pendingAuthorization, browserNonce, browserCSRF string, resultErr error) {
	var p pendingAuthorization
	id, randomErr := randomOAuthID()
	if randomErr != nil {
		return p, "", "", randomErr
	}
	nonce, randomErr := randomOAuthID()
	if randomErr != nil {
		return p, "", "", randomErr
	}
	csrf, randomErr := randomOAuthID()
	if randomErr != nil {
		return p, "", "", randomErr
	}
	nh, ch := sha256.Sum256([]byte(nonce)), sha256.Sum256([]byte(csrf))
	writeErr := s.oauthWrite(ctx, func(ctx context.Context) error {
		u := s.transaction(ctx)
		if !u.settings.Enabled {
			return ErrOAuthRequestUnavailable
		}
		resource := canonicalResource(u.settings.Port)
		if validationErr := validateAuthorizationForm(input, resource); validationErr != nil {
			return fosite.ErrInvalidRequest.WithWrap(validationErr)
		}
		if redirectErr := s.oauth.validateRedirect(ctx, input); redirectErr != nil {
			return redirectErr
		}
		// Registrations do not expire while clients are in use. Refresh the idle cleanup horizon.
		var name string
		if lookupErr := u.q.QueryRowContext(ctx, "SELECT name FROM oauth_clients WHERE id=? AND epoch=?", input.Get("client_id"), u.settings.Epoch).Scan(&name); lookupErr != nil {
			return oauthLookupError(lookupErr)
		}
		if _, err := u.q.ExecContext(ctx, "UPDATE oauth_clients SET expiry=? WHERE id=?", time.Now().Add(oauthGrantLifetime).UnixNano(), input.Get("client_id")); err != nil {
			return mcpStorageError(err)
		}
		form := cloneOAuthForm(input)
		if err := adaptOAuth(form, resource, false, true); err != nil {
			return err
		}
		req, _ := http.NewRequest(http.MethodGet, canonicalIssuer(u.settings.Port)+"/authorize?"+form.Encode(), http.NoBody)
		if _, err := s.oauth.provider.NewAuthorizeRequest(ctx, req); err != nil {
			return fmt.Errorf("OAuth authorization: %w", err)
		}
		if err := s.collectOAuth(ctx); err != nil {
			return err
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			return mcpStorageError(err)
		}
		now := time.Now().UTC()
		p = pendingAuthorization{OAuthPending: OAuthPending{ID: id, ClientID: input.Get("client_id"), ClientName: name, CorrelationCode: strings.ToUpper(id[:8]), Resource: resource, Scope: oauthScope, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), GrantLifetimeSeconds: int(oauthGrantLifetime.Seconds())}, form: cloneOAuthForm(input), epoch: u.settings.Epoch}
		_, err = u.q.ExecContext(ctx, "INSERT INTO oauth_pending(id,client,request,nonce_hash,csrf_hash,correlation,created,expiry,epoch,resource,status) VALUES(?,?,?,?,?,?,?,?,?,?,'pending')", id, p.ClientID, string(encoded), nh[:], ch[:], p.CorrelationCode, now.UnixNano(), p.ExpiresAt.UnixNano(), u.settings.Epoch, resource)
		return mcpStorageError(err)
	})
	if writeErr != nil {
		return pendingAuthorization{}, "", "", writeErr
	}
	return p, nonce, csrf, nil
}

// ListOAuthPending returns the bounded active ceremonies; no namespace is needed.
func (s *Store) ListOAuthPending(ctx context.Context) ([]OAuthPending, error) {
	out := []OAuthPending{}
	err := s.oauthView(ctx, func(ctx context.Context) error {
		rows, err := s.transaction(ctx).q.QueryContext(ctx, "SELECT "+pendingColumns+" FROM oauth_pending p JOIN oauth_clients c ON p.client=c.id WHERE p.status IN('pending','approved') AND p.expiry>? ORDER BY p.created,p.id", time.Now().UnixNano())
		if err != nil {
			return mcpStorageError(err)
		}
		for rows.Next() {
			p, e := scanPending(rows)
			if e != nil {
				err = e
				break
			}
			out = append(out, p.OAuthPending)
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
	return out, err
}

// ApproveOAuthPending must only be called by the trusted owner transport.
// Empty role means Observer; Developer requires an explicit owner selection.
func (s *Store) ApproveOAuthPending(ctx context.Context, id string, role Role, options ...OAuthApproval) error {
	if role == "" {
		role = Observer
	}
	if !role.Valid() {
		return errors.New("invalid OAuth role")
	}
	var option OAuthApproval
	if len(options) > 1 {
		return ErrOAuthApprovalInvalid
	}
	if len(options) == 1 {
		option = options[0]
	}
	option, deadline, err := option.validate(time.Now().UTC())
	if err != nil {
		return err
	}
	return s.decideOAuthPending(ctx, id, role, true, option.Name, deadline.UnixNano())
}

// DenyOAuthPending never issues credentials.
func (s *Store) DenyOAuthPending(ctx context.Context, id string) error {
	return s.decideOAuthPending(ctx, id, "", false, "", 0)
}
func (s *Store) decideOAuthPending(ctx context.Context, id string, role Role, approve bool, name string, deadline int64) error {
	return s.oauthWrite(ctx, func(ctx context.Context) error {
		u := s.transaction(ctx)
		if !u.settings.Enabled {
			return ErrOAuthRequestUnavailable
		}
		status := "denied"
		var approved, selected any
		if approve {
			status = "approved"
			approved = time.Now().UTC().UnixNano()
			selected = string(role)
		}
		r, err := u.q.ExecContext(ctx, "UPDATE oauth_pending SET status=?,role=?,approved=?,name=?,grant_deadline=? WHERE id=? AND status='pending' AND expiry>? AND epoch=? AND resource=?", status, selected, approved, name, deadline, id, time.Now().UnixNano(), u.settings.Epoch, canonicalResource(u.settings.Port))
		if err != nil {
			return mcpStorageError(err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return mcpStorageError(err)
		}
		if n != 1 {
			return ErrOAuthRequestUnavailable
		}
		return nil
	})
}

func (s *Store) browserPending(ctx context.Context, id, nonce string) (pendingAuthorization, error) {
	u := s.transaction(ctx)
	p, err := scanPending(u.q.QueryRowContext(ctx, "SELECT "+pendingColumns+" FROM oauth_pending p JOIN oauth_clients c ON p.client=c.id WHERE p.id=?", id))
	if err != nil {
		return p, err
	}
	hash := sha256.Sum256([]byte(nonce))
	if len(nonce) != 64 || subtle.ConstantTimeCompare(hash[:], p.nonceHash) != 1 || p.epoch != u.settings.Epoch || p.Resource != canonicalResource(u.settings.Port) || !u.settings.Enabled {
		return p, ErrOAuthRequestUnavailable
	}
	return p, nil
}

func (s *Store) finishAuthorization(ctx context.Context, id, nonce, csrf string) (string, error) {
	var callback string
	err := s.oauthWrite(ctx, func(ctx context.Context) error {
		p, err := s.browserPending(ctx, id, nonce)
		if err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(csrf))
		if len(csrf) != 64 || subtle.ConstantTimeCompare(hash[:], p.csrfHash) != 1 || p.Status == "pending" || p.Status == "completed" {
			return ErrOAuthRequestUnavailable
		}
		// Consume the ceremony and mint the code in the same transaction.
		if _, err = s.transaction(ctx).q.ExecContext(ctx, "UPDATE oauth_pending SET status='completed' WHERE id=?", id); err != nil {
			return mcpStorageError(err)
		}
		redirect, err := url.Parse(p.form.Get("redirect_uri"))
		if err != nil {
			return mcpStorageError(err)
		}
		query := redirect.Query()
		if p.Status == "approved" {
			if p.ApprovedAt == nil {
				return ErrOAuthRequestUnavailable
			}
			if !p.deadline.Valid || p.deadline.Int64 <= time.Now().UnixNano() {
				return ErrOAuthRequestUnavailable
			}
			deadline := time.Unix(0, p.deadline.Int64)
			code, e := s.oauth.mintApprovedCodeInTransaction(ctx, p.form, p.Role, *p.ApprovedAt, OAuthApproval{Name: p.name, ExpiresAt: &deadline})
			if e != nil {
				return e
			}
			query.Set("code", code)
		} else {
			query.Set("error", "access_denied")
		}
		if state := p.form.Get("state"); state != "" {
			query.Set("state", state)
		}
		query.Set("iss", canonicalIssuer(s.transaction(ctx).settings.Port))
		redirect.RawQuery = query.Encode()
		callback = redirect.String()
		return nil
	})
	if err != nil {
		return "", err
	}
	return callback, nil
}

// OAuthConnection is public connection metadata, including unverified name.
type OAuthConnection struct {
	OAuthGrant
	ClientName   string `json:"clientName"`
	Name         string `json:"name,omitempty"`
	NeverExpires bool   `json:"neverExpires,omitempty"`
	Scope        string `json:"scope"`
	Revoked      bool   `json:"revoked"`
}

// ListOAuthConnections includes unredeemed code grants and revoked connections.
func (s *Store) ListOAuthConnections(ctx context.Context) ([]OAuthConnection, error) {
	out := []OAuthConnection{}
	err := s.oauthView(ctx, func(ctx context.Context) error {
		rows, err := s.transaction(ctx).q.QueryContext(ctx, "SELECT g.id,g.client,c.name,g.role,g.resource,g.approved,g.deadline,g.revoked,g.revoked_at,g.last_used,g.name FROM oauth_grants g JOIN oauth_clients c ON g.client=c.id ORDER BY g.approved,g.id")
		if err != nil {
			return mcpStorageError(err)
		}
		for rows.Next() {
			var c OAuthConnection
			var a, d int64
			var revoked, last sql.NullInt64
			if err = rows.Scan(&c.ID, &c.ClientID, &c.ClientName, &c.Role, &c.Resource, &a, &d, &c.Revoked, &revoked, &last, &c.Name); err != nil {
				break
			}
			c.ApprovedAt = time.Unix(0, a).UTC()
			c.NeverExpires = d == oauthNoDeadline
			if !c.NeverExpires {
				c.ExpiresAt = time.Unix(0, d).UTC()
			}
			c.RevokedAt = tokenTime(revoked)
			c.LastUsedAt = tokenTime(last)
			c.Scope = oauthScope
			out = append(out, c)
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
	return out, err
}

// RevokeOAuthConnection denies the stable grant, including its artifacts. A
// failed durable write is returned AND retained as process-local revocation debt.
func (s *Store) RevokeOAuthConnection(ctx context.Context, id string) error {
	if id == "" || len(id) > 128 {
		return errors.New("invalid OAuth connection id")
	}
	return s.writeRevocation(ctx, id, func(ctx context.Context) error {
		var exists bool
		if err := s.transaction(ctx).q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM oauth_grants WHERE id=?)", id).Scan(&exists); err != nil {
			return mcpStorageError(err)
		}
		if !exists {
			return fosite.ErrNotFound
		}
		return s.oauth.storage.revoke(ctx, id)
	})
}
