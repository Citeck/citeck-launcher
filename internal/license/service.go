package license

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// SecretType used to store license JSON blobs in the SecretService. We carve
// out a dedicated type so ListSecrets can be filtered without scanning
// values. Stored as a sentinel string so the wire format stays stable.
const SecretType storage.SecretType = "LICENSE"

// SecretIDPrefix prefixes every stored license secret. It guarantees a clean
// namespace ("lic:<id>") and avoids collisions with regular auth secrets.
const SecretIDPrefix = "lic:"

// Service is the runtime entry point for managing licenses. It wraps the
// existing SecretService so license blobs are encrypted at rest in the same
// envelope the launcher uses for auth tokens.
//
// The Kotlin equivalent kept all licenses inside a single SecretsStorage list
// keyed by "licenses". The Go port stores one license per secret (id =
// SecretIDPrefix + license.ID) so listing and individual deletion stay O(1)
// against the SQLite store.
type Service struct {
	secrets *storage.SecretService

	mu     sync.Mutex
	cached []Instance // cached merge of stored + embedded; rebuilt on mutation
	dirty  bool
}

// NewService constructs a license service over the given SecretService.
func NewService(secrets *storage.SecretService) *Service {
	return &Service{secrets: secrets, dirty: true}
}

// List returns all stored licenses sorted by descending priority (so the
// highest-priority license is the head). Mirrors Kotlin's natural iteration
// order in callers that pick the "best" license.
func (s *Service) List() ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		out := make([]Instance, len(s.cached))
		copy(out, s.cached)
		return out, nil
	}

	metas, err := s.secrets.ListSecrets()
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	out := make([]Instance, 0, len(metas))
	for _, meta := range metas {
		if meta.Type != SecretType {
			continue
		}
		sec, getErr := s.secrets.GetSecret(meta.ID)
		if getErr != nil {
			// Likely "secrets locked"; surface only the first error.
			return nil, fmt.Errorf("get license %s: %w", meta.ID, getErr)
		}
		var lic Instance
		if jsonErr := json.Unmarshal([]byte(sec.Value), &lic); jsonErr != nil {
			return nil, fmt.Errorf("decode license %s: %w", meta.ID, jsonErr)
		}
		out = append(out, lic)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Priority > out[j].Priority
	})
	s.cached = out
	s.dirty = false
	clone := make([]Instance, len(out))
	copy(clone, out)
	return clone, nil
}

// HasValidEnterprise mirrors Kotlin's hasValidEntLicense(): returns true iff
// at least one stored license validates against the current clock.
func (s *Service) HasValidEnterprise() bool {
	licenses, err := s.List()
	if err != nil {
		return false
	}
	for _, lic := range licenses {
		if lic.IsValid() {
			return true
		}
	}
	return false
}

// Add stores a license. The caller is responsible for validation (the daemon
// route calls IsValid before storage if it wants to reject expired
// licenses); Add itself accepts any well-formed instance so legacy/expired
// records can also be archived if needed.
func (s *Service) Add(lic Instance) error {
	if lic.ID == "" {
		return errors.New("license id is required")
	}
	body, err := json.Marshal(lic)
	if err != nil {
		return fmt.Errorf("marshal license: %w", err)
	}
	secret := storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:        SecretIDPrefix + lic.ID,
			Name:      summary(lic),
			Type:      SecretType,
			Scope:     lic.Tenant,
			CreatedAt: time.Now().UTC(),
		},
		Value: string(body),
	}
	if err := s.secrets.SaveSecret(secret); err != nil {
		return fmt.Errorf("save license: %w", err)
	}
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
	return nil
}

// Delete removes a license by its ID. Idempotent: deleting a missing license
// is not an error, mirroring the Kotlin secrets storage semantics.
func (s *Service) Delete(id string) error {
	if id == "" {
		return errors.New("license id is required")
	}
	if err := s.secrets.DeleteSecret(SecretIDPrefix + id); err != nil {
		return fmt.Errorf("delete license: %w", err)
	}
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
	return nil
}

// Refresh forces a re-read of the underlying store on next List() call. Use
// after the SecretService is unlocked so cached "licenses are missing"
// results don't survive an unlock.
func (s *Service) Refresh() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

func summary(lic Instance) string {
	parts := []string{lic.Tenant}
	if lic.IssuedTo != "" {
		parts = append(parts, lic.IssuedTo)
	}
	if !lic.ValidUntil.IsZero() {
		parts = append(parts, "until "+lic.ValidUntil.UTC().Format("2006-01-02"))
	}
	s := strings.Join(parts, " · ")
	if s == "" {
		return lic.ID
	}
	return s
}
