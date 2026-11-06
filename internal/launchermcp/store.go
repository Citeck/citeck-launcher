// Package launchermcp implements the launcher's separate, local MCP surface.
package launchermcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Role is exactly one of the two fixed instance-wide authority levels.
type Role string

// The default port is inactive until the owner explicitly enables MCP.
const (
	Observer    Role = "observer"
	Developer   Role = "developer"
	DefaultPort      = 19380

	// Owner-selected finite credentials share one bounded validity policy.
	maxCredentialLifetime = 36500 * 24 * time.Hour
)

// Valid accepts only the two fixed roles, never an unknown/custom role.
func (r Role) Valid() bool { return r == Observer || r == Developer }

// Token is public metadata; the random credential and hash are intentionally absent.
type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Role       Role       `json:"role"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}
type tokenRecord struct {
	Token
	Hash string `json:"hash"`
}

// Settings is daemon-global and has a fail-closed absent-enabled zero value.
type Settings struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}
type state struct {
	Version  int             `json:"version,omitempty"`
	OAuth    json.RawMessage `json:"oauth,omitempty"`
	Settings Settings        `json:"settings"`
	Tokens   []tokenRecord   `json:"tokens"`
}

// Store owns one private MCP database, independently of the launcher store and vault.
// The mutex serializes the outer Fosite unit of work and its revocation debt.
type Store struct {
	mu                   sync.Mutex
	db                   *sql.DB
	path                 string
	dir                  string
	lock                 *os.File
	key                  []byte
	settingsCache        Settings
	oauth                *oauthProvider
	revocationDebt       map[string]time.Time
	revocationRetryAt    time.Time
	revocationRetryDelay time.Duration
	revocationWarning    string
	lastUsedAttempt      map[string]time.Time
	closed               bool
	uncertain            bool
	// beforeCommit is a package-private fault seam; it never replaces SQL authority.
	beforeCommit func() error
}

// Settings is the last confirmed configuration, not a listener-health claim.
func (s *Store) Settings() Settings { s.mu.Lock(); defer s.mu.Unlock(); return s.settingsCache }

// SetSettings commits enable intent and OAuth invalidation atomically.
func (s *Store) SetSettings(v Settings) error {
	if v.Port < 1024 || v.Port > 65535 {
		return errors.New("port must be 1024..65535")
	}
	return s.oauthWrite(context.Background(), func(ctx context.Context) error {
		u := s.transaction(ctx)
		if _, err := u.q.ExecContext(ctx, "UPDATE mcp_settings SET enabled=?,port=?,epoch=epoch+? WHERE id=1", v.Enabled, v.Port, v.Port != u.settings.Port); err != nil {
			return mcpStorageError(err)
		}
		if v.Port != u.settings.Port {
			// Old issuers are permanently invalidated, even after a port ABA. Keep
			// connection metadata and data, rather than silently deleting real grants.
			if _, err := u.q.ExecContext(ctx, "UPDATE oauth_grants SET revoked=1,revoked_at=coalesce(revoked_at,?)", time.Now().UnixNano()); err != nil {
				return mcpStorageError(err)
			}
			if _, err := u.q.ExecContext(ctx, "UPDATE oauth_clients SET epoch=epoch+1"); err != nil {
				return mcpStorageError(err)
			}
		}
		if !v.Enabled || v.Port != u.settings.Port {
			// Unredeemed codes never survive disabling the listener.
			if _, err := u.q.ExecContext(ctx, "UPDATE oauth_grants SET revoked=1,revoked_at=coalesce(revoked_at,?) WHERE credential_issued_at IS NULL", time.Now().UnixNano()); err != nil {
				return mcpStorageError(err)
			}
			for _, q := range []string{
				"UPDATE oauth_pending SET status='cancelled' WHERE status IN('pending','approved')",
				"DELETE FROM oauth_pkce", "DELETE FROM oauth_codes",
			} {
				if _, err := u.q.ExecContext(ctx, q); err != nil {
					return mcpStorageError(err)
				}
			}
		}
		return nil
	})
}

func tokenTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(0, v.Int64).UTC()
	return &t
}
func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var created int64
	var expiry, revoked, last sql.NullInt64
	err := row.Scan(&t.ID, &t.Name, &t.Role, &created, &expiry, &revoked, &last)
	t.CreatedAt = time.Unix(0, created).UTC()
	t.ExpiresAt = tokenTime(expiry)
	t.RevokedAt = tokenTime(revoked)
	t.LastUsedAt = tokenTime(last)
	return t, mcpStorageError(err)
}

//nolint:gosec // SQL column names, not credential values.
const tokenColumns = "id,name,role,created,expiry,revoked_at,last_used"

// List returns public metadata only, never hashes or credentials.
func (s *Store) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Token{}
	if s.closed || s.uncertain {
		return out
	}
	rows, err := s.db.Query("SELECT " + tokenColumns + " FROM manual_tokens ORDER BY created,id")
	if err != nil {
		s.uncertain = true
		return out
	}
	for rows.Next() {
		t, e := scanToken(rows)
		if e != nil {
			err = e
			break
		}
		out = append(out, t)
	}
	if errors.Join(err, rows.Err(), rows.Close()) != nil {
		s.uncertain = true
		return []Token{}
	}
	return out
}

// Create returns a random credential only after its hash commits.
func (s *Store) Create(name string, role Role, expires *time.Time) (Token, string, error) {
	if !role.Valid() || len(name) < 1 || len(name) > 100 {
		return Token{}, "", errors.New("a name (1..100 characters) and observer/developer role are required")
	}
	now := time.Now().UTC()
	if expires != nil && (!expires.After(now) || expires.After(now.Add(maxCredentialLifetime))) {
		return Token{}, "", errors.New("expiry must be in the future and within 36500 days")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, "", fmt.Errorf("MCP credential: %w", err)
	}
	value := "citeck_mcp_" + hex.EncodeToString(raw)
	h := sha256.Sum256([]byte(value))
	id, err := randomOAuthID()
	if err != nil {
		return Token{}, "", err
	}
	t := Token{ID: id, Name: name, Role: role, CreatedAt: now, ExpiresAt: expires}
	err = s.oauthWrite(context.Background(), func(ctx context.Context) error {
		_, e := s.transaction(ctx).q.ExecContext(ctx, "INSERT INTO manual_tokens(id,name,hash,role,created,expiry) VALUES(?,?,?,?,?,?)", t.ID, t.Name, h[:], t.Role, t.CreatedAt.UnixNano(), stamp(expires))
		return mcpStorageError(e)
	})
	if err != nil {
		return Token{}, "", err
	}
	return t, value, nil
}

// Revoke marks a manual credential revoked only after its write commits.
func (s *Store) Revoke(id string) error {
	return s.oauthWrite(context.Background(), func(ctx context.Context) error {
		r, e := s.transaction(ctx).q.ExecContext(ctx, "UPDATE manual_tokens SET revoked_at=coalesce(revoked_at,?) WHERE id=?", time.Now().UnixNano(), id)
		if e != nil {
			return mcpStorageError(e)
		}
		n, e := r.RowsAffected()
		if e != nil {
			return mcpStorageError(e)
		}
		if n != 1 {
			return errors.New("token not found")
		}
		return nil
	})
}

// Authenticate checks the hash and authority, then throttles last-used writes.
func (s *Store) Authenticate(value string) (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.uncertain || len(value) > 512 {
		return Token{}, false
	}
	h := sha256.Sum256([]byte(value))
	t, err := scanToken(s.db.QueryRow("SELECT "+tokenColumns+" FROM manual_tokens WHERE hash=?", h[:]))
	now := time.Now().UTC()
	if err != nil || !t.Role.Valid() || t.RevokedAt != nil || t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return Token{}, false
	}
	if s.lastUsedAttempt == nil {
		s.lastUsedAttempt = map[string]time.Time{}
	}
	if (t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= time.Minute) && now.Sub(s.lastUsedAttempt[t.ID]) >= time.Minute {
		s.lastUsedAttempt[t.ID] = now
		_ = s.writeLocked(context.Background(), "", func(ctx context.Context) error {
			_, e := s.transaction(ctx).q.ExecContext(ctx, "UPDATE manual_tokens SET last_used=? WHERE id=? AND (last_used IS NULL OR last_used<=?)", now.UnixNano(), t.ID, now.Add(-time.Minute).UnixNano())
			return mcpStorageError(e)
		})
	}
	if s.uncertain {
		return Token{}, false
	}
	return t, true
}

// Close is called after the Service has stopped its requests and retry worker.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.db.Close(), s.lock.Close())
}
