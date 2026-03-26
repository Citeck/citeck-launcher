package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// FileStore implements Store using flat files. Used in server mode.
// Workspaces are not used in server mode (single namespace), but the interface
// is satisfied with a no-op default workspace.
type FileStore struct {
	baseDir string
	mu      sync.RWMutex
}

// NewFileStore creates a FileStore rooted at baseDir.
func NewFileStore(baseDir string) (*FileStore, error) {
	secretsDir := filepath.Join(baseDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}
	return &FileStore{baseDir: baseDir}, nil
}

// --- Workspaces (server mode: single implicit workspace) ---

const defaultWorkspaceID = "daemon"

func (s *FileStore) ListWorkspaces() ([]WorkspaceDto, error) {
	return []WorkspaceDto{{ID: defaultWorkspaceID, Name: "Default"}}, nil
}

func (s *FileStore) GetWorkspace(id string) (*WorkspaceDto, error) {
	if id == defaultWorkspaceID {
		return &WorkspaceDto{ID: defaultWorkspaceID, Name: "Default"}, nil
	}
	return nil, fmt.Errorf("workspace %q not found", id)
}

func (s *FileStore) SaveWorkspace(_ WorkspaceDto) error {
	return nil // no-op in server mode
}

func (s *FileStore) DeleteWorkspace(_ string) error {
	return nil // no-op in server mode
}

// --- Secrets ---

func (s *FileStore) secretsDir() string {
	return filepath.Join(s.baseDir, "secrets")
}

func (s *FileStore) secretPath(id string) string {
	return filepath.Join(s.secretsDir(), id+".json")
}

func (s *FileStore) ListSecrets() ([]SecretMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.secretsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []SecretMeta
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		secret, err := s.readSecret(filepath.Join(s.secretsDir(), entry.Name()))
		if err != nil {
			slog.Warn("Failed to read secret file", "file", entry.Name(), "err", err)
			continue
		}
		result = append(result, secret.SecretMeta)
	}
	return result, nil
}

func (s *FileStore) GetSecret(id string) (*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readSecret(s.secretPath(id))
}

func (s *FileStore) SaveSecret(secret Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if secret.Scope == "" {
		secret.Scope = "global"
	}

	// Preserve original created_at on update (consistent with SQLiteStore)
	existing, err := s.readSecret(s.secretPath(secret.ID))
	if err == nil && !existing.CreatedAt.IsZero() {
		secret.CreatedAt = existing.CreatedAt
	} else if secret.CreatedAt.IsZero() {
		secret.CreatedAt = time.Now()
	}

	data, err := json.MarshalIndent(secretFile{
		ID:        secret.ID,
		Name:      secret.Name,
		Type:      secret.Type,
		Value:     secret.Value,
		Scope:     secret.Scope,
		CreatedAt: secret.CreatedAt,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secret: %w", err)
	}

	return fsutil.AtomicWriteFile(s.secretPath(secret.ID), data, 0o600)
}

func (s *FileStore) DeleteSecret(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.secretPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// --- Launcher State ---

func (s *FileStore) statePath() string {
	return filepath.Join(s.baseDir, "state.json")
}

func (s *FileStore) GetState() (*LauncherState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &LauncherState{}, nil
		}
		return nil, err
	}
	var state LauncherState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *FileStore) SetState(state LauncherState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return fsutil.AtomicWriteFile(s.statePath(), data, 0o644)
}

func (s *FileStore) Close() error {
	return nil
}

// secretFile is the on-disk JSON format (includes Value).
type secretFile struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      SecretType `json:"type"`
	Value     string     `json:"value"`
	Scope     string     `json:"scope"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *FileStore) readSecret(path string) (*Secret, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sf secretFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	return &Secret{
		SecretMeta: SecretMeta{
			ID:        sf.ID,
			Name:      sf.Name,
			Type:      sf.Type,
			Scope:     sf.Scope,
			CreatedAt: sf.CreatedAt,
		},
		Value: sf.Value,
	}, nil
}
