// Package launchermcp implements the launcher's separate, local MCP surface.
// It never serves the daemon API mux and never starts a daemon.
package launchermcp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// Role is exactly one of the two fixed instance-wide authority levels.
type Role string

// The default port is inactive until the owner explicitly enables MCP.
const (
	Observer    Role = "observer"
	Developer   Role = "developer"
	DefaultPort      = 19380
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
	Settings Settings      `json:"settings"`
	Tokens   []tokenRecord `json:"tokens"`
}

// Store is independent of the secret vault and workspace. Only random-token
// hashes are persisted; public token metadata never includes those hashes.
type Store struct {
	mu              sync.Mutex
	path            string
	state           state
	write           func(string, []byte, os.FileMode) error
	lastUsedAttempt map[string]time.Time
}

// OpenStore reads owner-only state independently of the user-secret vault.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, write: fsutil.AtomicWriteFile, state: state{Settings: Settings{Port: DefaultPort}}}
	if operationErr := os.MkdirAll(filepath.Dir(path), 0o700); operationErr != nil {
		return nil, fmt.Errorf("store: %w", operationErr)
	}
	if operationErr := privatePermissions(filepath.Dir(path), true); operationErr != nil {
		return nil, operationErr
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if operationErr := privatePermissions(path, false); operationErr != nil {
		return nil, operationErr
	}
	f, err := os.Open(path) //nolint:gosec // G304: private daemon-owned token file, validated owner-only directory and regular leaf above.
	if err != nil {
		return nil, fmt.Errorf("read MCP state: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if len(b) > 1<<20 {
		return nil, errors.New("MCP state too large")
	}
	if err = json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("decode MCP state: %w", err)
	}
	if s.state.Settings.Port < 1024 || s.state.Settings.Port > 65535 {
		return nil, errors.New("invalid MCP port")
	}
	for _, t := range s.state.Tokens {
		h, e := hex.DecodeString(t.Hash)
		if !t.Role.Valid() || e != nil || len(h) != sha256.Size {
			return nil, errors.New("invalid MCP token record")
		}
	}
	return s, nil
}
func (s *Store) save(next state) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if len(b) > 1<<20 {
		return errors.New("MCP token storage full")
	}
	if operationErr := os.MkdirAll(filepath.Dir(s.path), 0o700); operationErr != nil {
		return fmt.Errorf("store: %w", operationErr)
	}
	if operationErr := privatePermissions(filepath.Dir(s.path), true); operationErr != nil {
		return operationErr
	}
	if err = s.write(s.path, b, 0o600); err != nil {
		return fmt.Errorf("persist MCP state: %w", err)
	}
	s.state = next
	return nil
}

// Settings returns the persisted enable intent, not a listener-health claim.
func (s *Store) Settings() Settings { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Settings }

// SetSettings publishes settings only after atomic persistence succeeds.
func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Port < 1024 || v.Port > 65535 {
		return errors.New("port must be 1024..65535")
	}
	n := s.state
	n.Settings = v
	return s.save(n)
}

// List returns metadata only; token values are never recoverable.
func (s *Store) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Token, 0, len(s.state.Tokens))
	for _, t := range s.state.Tokens {
		out = append(out, t.Token)
	}
	return out
}

// Create returns a random credential once, only after its hash is persisted.
func (s *Store) Create(name string, role Role, expires *time.Time) (Token, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !role.Valid() || len(name) < 1 || len(name) > 100 {
		return Token{}, "", errors.New("a name (1..100 characters) and observer/developer role are required")
	}
	now := time.Now().UTC()
	if expires != nil && !expires.After(now) {
		return Token{}, "", errors.New("expiry must be in the future")
	}
	if len(s.state.Tokens) >= 1000 {
		return Token{}, "", errors.New("MCP token limit reached")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, "", fmt.Errorf("store: %w", err)
	}
	value := "citeck_mcp_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(value))
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Token{}, "", fmt.Errorf("store: %w", err)
	}
	t := Token{ID: hex.EncodeToString(id), Name: name, Role: role, CreatedAt: now, ExpiresAt: expires}
	n := s.state
	n.Tokens = append(append([]tokenRecord(nil), n.Tokens...), tokenRecord{Token: t, Hash: hex.EncodeToString(hash[:])})
	if err := s.save(n); err != nil {
		return Token{}, "", err
	}
	return t, value, nil
}

// Revoke does not claim success or change memory if persistence fails.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.state
	n.Tokens = append([]tokenRecord(nil), n.Tokens...)
	for i := range n.Tokens {
		if n.Tokens[i].ID == id {
			now := time.Now().UTC()
			n.Tokens[i].RevokedAt = &now
			return s.save(n)
		}
	}
	return errors.New("token not found")
}

// Authenticate rechecks expiry/revocation and rate-limits last-used persistence.
func (s *Store) Authenticate(value string) (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := sha256.Sum256([]byte(value))
	now := time.Now().UTC()
	for i, t := range s.state.Tokens {
		stored, err := hex.DecodeString(t.Hash)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(hash[:], stored) != 1 {
			continue
		}
		if !t.Role.Valid() || t.RevokedAt != nil || (t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)) {
			return Token{}, false
		}
		if s.lastUsedAttempt == nil {
			s.lastUsedAttempt = map[string]time.Time{}
		}
		lastAttempt := s.lastUsedAttempt[t.ID]
		if (t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= time.Minute) && now.Sub(lastAttempt) >= time.Minute {
			s.lastUsedAttempt[t.ID] = now
			n := s.state
			n.Tokens = append([]tokenRecord(nil), n.Tokens...)
			n.Tokens[i].LastUsedAt = &now
			_ = s.save(n)
		}
		return t.Token, true
	}
	return Token{}, false
}
