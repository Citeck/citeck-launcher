package launchermcp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"time"

	"github.com/ory/fosite"
)

const (
	oauthScope         = "mcp"
	oauthGrantLifetime = 30 * 24 * time.Hour
)

type oauthClient struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Redirects []string  `json:"redirects"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// OAuthGrant is connection metadata, never an access or refresh credential.
// Role is owner-selected; neither client registration nor scopes assign it.
type OAuthGrant struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"clientId"`
	Role       Role       `json:"role"`
	Resource   string     `json:"resource"`
	ApprovedAt time.Time  `json:"approvedAt"`
	ExpiresAt  time.Time  `json:"expiresAt,omitzero"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// SetExpiresAt caps both initial issuance and refresh at the immutable approval
// deadline, including Fosite's later expiry setters and cloned sessions.
type oauthSession struct {
	GrantID   string                         `json:"grantId"`
	Deadline  time.Time                      `json:"deadline"`
	ExpiresAt map[fosite.TokenType]time.Time `json:"expiresAt"`
}

func (s *oauthSession) SetExpiresAt(kind fosite.TokenType, at time.Time) {
	if s.ExpiresAt == nil {
		s.ExpiresAt = map[fosite.TokenType]time.Time{}
	}
	if kind == fosite.RefreshToken || at.After(s.Deadline) {
		at = s.Deadline
	}
	s.ExpiresAt[kind] = at
}
func (s *oauthSession) GetExpiresAt(kind fosite.TokenType) time.Time { return s.ExpiresAt[kind] }
func (*oauthSession) GetUsername() string                            { return "" }
func (s *oauthSession) GetSubject() string                           { return s.GrantID }
func (s *oauthSession) Clone() fosite.Session {
	cloned := *s
	cloned.ExpiresAt = maps.Clone(s.ExpiresAt)
	return &cloned
}

func canonicalIssuer(port int) string   { return "http://127.0.0.1:" + strconv.Itoa(port) }
func canonicalResource(port int) string { return canonicalIssuer(port) + "/mcp" }

func validOAuthRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" {
		return false
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && u.String() == raw
}

// Native IP loopback callbacks may choose a port per request (RFC 8252).
// Everything except that port stays byte-exact; localhost gets exact matching.
func matchesOAuthRedirect(registered []string, actual string) bool {
	if !validOAuthRedirect(actual) {
		return false
	}
	requested, _ := url.Parse(actual)
	for _, raw := range registered {
		if raw == actual {
			return true
		}
		r, err := url.Parse(raw)
		if err != nil || r.Hostname() != requested.Hostname() || (r.Hostname() != "127.0.0.1" && r.Hostname() != "::1") {
			continue
		}
		r.Host = requested.Host
		if r.String() == actual {
			return true
		}
	}
	return false
}

func validateOAuthClient(id string, c oauthClient) error {
	if id == "" || id != c.ID || len(id) > 128 || len(c.Name) > 100 || len(c.Redirects) < 1 || len(c.Redirects) > 8 || c.CreatedAt.IsZero() || !c.ExpiresAt.After(c.CreatedAt) {
		return errors.New("invalid MCP OAuth client")
	}
	for _, redirect := range c.Redirects {
		if len(redirect) > 2048 || !validOAuthRedirect(redirect) {
			return errors.New("invalid MCP OAuth redirect")
		}
	}
	return nil
}
func randomOAuthID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("MCP OAuth identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}
