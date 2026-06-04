package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// FileStore implements Store using flat files. Used in server mode.
// Workspaces are not used in server mode (single namespace), but the interface
// is satisfied with a no-op default workspace.
type FileStore struct {
	baseDir     string // conf dir: holds namespace.yml + secrets/ + *.json
	runtimeBase string // server runtime root: {DataDir}/runtime ; per-NS state lives at {runtimeBase}/{nsID}/state-{nsID}.json
	mu          sync.RWMutex
}

// NewFileStore creates a FileStore rooted at baseDir with runtime state at runtimeBase.
func NewFileStore(baseDir, runtimeBase string) (*FileStore, error) {
	secretsDir := filepath.Join(baseDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}
	return &FileStore{baseDir: baseDir, runtimeBase: runtimeBase}, nil
}

// --- Workspaces (server mode: single implicit workspace) ---

const defaultWorkspaceID = "daemon"

// ListWorkspaces returns the single implicit "daemon" workspace.
func (s *FileStore) ListWorkspaces() ([]WorkspaceDto, error) {
	return []WorkspaceDto{{ID: defaultWorkspaceID, Name: "Default"}}, nil
}

// GetWorkspace returns the default workspace if the ID matches.
func (s *FileStore) GetWorkspace(id string) (*WorkspaceDto, error) {
	if id == defaultWorkspaceID {
		return &WorkspaceDto{ID: defaultWorkspaceID, Name: "Default"}, nil
	}
	return nil, fmt.Errorf("workspace %q not found", id)
}

// SaveWorkspace is a no-op in server mode.
func (s *FileStore) SaveWorkspace(_ WorkspaceDto) error {
	return nil // no-op in server mode
}

// DeleteWorkspace is a no-op in server mode.
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

// ListSecrets returns metadata for all secrets stored as JSON files.
func (s *FileStore) ListSecrets() ([]SecretMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.secretsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list secrets dir: %w", err)
	}

	result := make([]SecretMeta, 0, len(entries))
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

// GetSecret returns a secret including its value.
func (s *FileStore) GetSecret(id string) (*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readSecret(s.secretPath(id))
}

// SaveSecret writes a secret to a JSON file, preserving the original creation time on update.
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
		Username:  secret.Username,
		Value:     secret.Value,
		Scope:     secret.Scope,
		CreatedAt: secret.CreatedAt,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secret: %w", err)
	}

	if err := fsutil.AtomicWriteFile(s.secretPath(secret.ID), data, 0o600); err != nil {
		return fmt.Errorf("write secret %s: %w", secret.ID, err)
	}
	return nil
}

// DeleteSecret removes a secret JSON file.
func (s *FileStore) DeleteSecret(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.secretPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete secret %s: %w", id, err)
	}
	return nil
}

// --- Launcher State ---

func (s *FileStore) statePath() string {
	return filepath.Join(s.baseDir, "state.json")
}

// GetState returns the persisted launcher state from state.json.
//
// Legacy state.json files written by v2.0 stored a single namespaceId field;
// they're folded into SelectedNs[WorkspaceID] on read so the next SetState
// upgrades the on-disk shape transparently.
func (s *FileStore) GetState() (*LauncherState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &LauncherState{}, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var raw struct {
		WorkspaceID string            `json:"workspaceId"`
		NamespaceID string            `json:"namespaceId,omitempty"`
		SelectedNs  map[string]string `json:"selectedNs,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse state JSON: %w", err)
	}
	state := LauncherState{WorkspaceID: raw.WorkspaceID, SelectedNs: raw.SelectedNs}
	if raw.NamespaceID != "" && raw.WorkspaceID != "" {
		if state.SelectedNs == nil {
			state.SelectedNs = make(map[string]string, 1)
		}
		if _, ok := state.SelectedNs[raw.WorkspaceID]; !ok {
			state.SelectedNs[raw.WorkspaceID] = raw.NamespaceID
		}
	}
	return &state, nil
}

// SetState persists the launcher state to state.json atomically.
func (s *FileStore) SetState(state LauncherState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := fsutil.AtomicWriteFile(s.statePath(), data, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// PutSecretBlob stores the encrypted secrets blob to a file.
func (s *FileStore) PutSecretBlob(base64Data string) error {
	if err := fsutil.AtomicWriteFile(filepath.Join(s.baseDir, "secret_blob.dat"), []byte(base64Data), 0o600); err != nil {
		return fmt.Errorf("write secret blob: %w", err)
	}
	return nil
}

// GetSecretBlob retrieves the encrypted secrets blob from a file.
func (s *FileStore) GetSecretBlob() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, "secret_blob.dat"))
	if err != nil {
		return "", fmt.Errorf("read secret blob: %w", err)
	}
	return string(data), nil
}

// --- Key-Value State ---

func (s *FileStore) kvStatePath() string {
	return filepath.Join(s.baseDir, "kv-state.json")
}

func (s *FileStore) readKVState() (map[string]string, error) {
	data, err := os.ReadFile(s.kvStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("read kv-state: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse kv-state: %w", err)
	}
	return m, nil
}

// GetStateValue reads a single key from kv-state.json. Returns "" if not found.
func (s *FileStore) GetStateValue(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readKVState()
	if err != nil {
		return "", err
	}
	return m[key], nil
}

// SetStateValue writes a single key to kv-state.json (read-modify-write).
func (s *FileStore) SetStateValue(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readKVState()
	if err != nil {
		return err
	}
	if value == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal kv-state: %w", err)
	}
	if err = fsutil.AtomicWriteFile(s.kvStatePath(), data, 0o600); err != nil {
		return fmt.Errorf("write kv-state: %w", err)
	}
	return nil
}

// --- Git Repo State ---

func (s *FileStore) gitRepoStatePath() string {
	return filepath.Join(s.baseDir, "git-repo-state.json")
}

func (s *FileStore) readGitRepoState() (map[string]GitRepoState, error) {
	data, err := os.ReadFile(s.gitRepoStatePath()) //nolint:gosec // G304: path is constructed from baseDir
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]GitRepoState), nil
		}
		return nil, fmt.Errorf("read git-repo-state: %w", err)
	}
	var m map[string]GitRepoState
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse git-repo-state: %w", err)
	}
	if m == nil {
		m = make(map[string]GitRepoState)
	}
	return m, nil
}

// GetGitRepoState returns the persisted sync metadata for a repo path, or
// (nil, nil) when no row exists.
func (s *FileStore) GetGitRepoState(path string) (*GitRepoState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readGitRepoState()
	if err != nil {
		return nil, err
	}
	state, ok := m[path]
	if !ok {
		return nil, nil
	}
	return &state, nil
}

// SetGitRepoState upserts a git repo state entry (read-modify-write).
func (s *FileStore) SetGitRepoState(state GitRepoState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readGitRepoState()
	if err != nil {
		return err
	}
	m[state.Path] = state
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal git-repo-state: %w", err)
	}
	if err := fsutil.AtomicWriteFile(s.gitRepoStatePath(), data, 0o600); err != nil {
		return fmt.Errorf("write git-repo-state: %w", err)
	}
	return nil
}

// ListGitRepoStates returns all git repo state rows.
func (s *FileStore) ListGitRepoStates() ([]GitRepoState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readGitRepoState()
	if err != nil {
		return nil, err
	}
	out := make([]GitRepoState, 0, len(m))
	for _, st := range m {
		out = append(out, st)
	}
	return out, nil
}

// --- Namespaces (server-mode single-namespace file mapping) ---
//
// Server mode keeps the single namespace as conf/namespace.yml and runtime
// state as {runtimeBase}/{nsID}/state-{nsID}.json, so the daemon can use the
// Store API uniformly while the on-disk bytes (hand-editable, written by
// `citeck setup`) stay exactly as before. The wsID param is ignored — server
// mode has one implicit "daemon" workspace.

func (s *FileStore) nsConfigPath() string { return filepath.Join(s.baseDir, "namespace.yml") }

func (s *FileStore) nsStatePath(nsID string) string {
	return filepath.Join(s.runtimeBase, nsID, "state-"+nsID+".json")
}

func (s *FileStore) ListNamespaces(_ string) ([]NamespaceRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.nsConfigPath()) //nolint:gosec // path is conf/namespace.yml, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read namespace config: %w", err)
	}
	var m struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse namespace config: %w", err)
	}
	if m.ID == "" {
		m.ID = "default"
	}
	return []NamespaceRow{{ID: m.ID, Name: m.Name}}, nil
}

func (s *FileStore) LoadNamespaceConfig(_, _ string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.nsConfigPath()) //nolint:gosec // path is conf/namespace.yml
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read namespace config: %w", err)
	}
	return string(data), true, nil
}

func (s *FileStore) SaveNamespaceConfig(_, _, _, configYAML string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fsutil.AtomicWriteFile(s.nsConfigPath(), []byte(configYAML), 0o644); err != nil {
		return fmt.Errorf("write namespace config: %w", err)
	}
	return nil
}

func (s *FileStore) LoadNamespaceState(_, nsID string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.nsStatePath(nsID)) //nolint:gosec // path from runtimeBase + nsID
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read namespace state: %w", err)
	}
	return string(data), true, nil
}

func (s *FileStore) SaveNamespaceState(_, nsID, _, stateJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.nsStatePath(nsID)
	_ = os.MkdirAll(filepath.Dir(p), 0o750)
	if err := fsutil.AtomicWriteFile(p, []byte(stateJSON), 0o644); err != nil {
		return fmt.Errorf("write namespace state: %w", err)
	}
	return nil
}

func (s *FileStore) DeleteNamespace(_, nsID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.nsConfigPath())
	_ = os.Remove(s.nsStatePath(nsID))
	return nil
}

// Close is a no-op for FileStore (no resources to release).
func (s *FileStore) Close() error {
	return nil
}

// secretFile is the on-disk JSON format (includes Value).
type secretFile struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      SecretType `json:"type"`
	Username  string     `json:"username,omitempty"`
	Value     string     `json:"value"`
	Scope     string     `json:"scope"`
	CreatedAt time.Time  `json:"createdAt"`
}

func (s *FileStore) readSecret(path string) (*Secret, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from internal baseDir + secret ID
	if err != nil {
		return nil, fmt.Errorf("read secret file %s: %w", path, err)
	}
	var sf secretFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse secret JSON %s: %w", path, err)
	}
	username, value := sf.Username, sf.Value
	if username == "" && (sf.Type == SecretBasicAuth || sf.Type == SecretRegistryAuth) {
		// Legacy on-disk format: "user:pass" packed into Value.
		// Base64-encoded ciphertext never contains ':', so this only fires on
		// plaintext rows written before the typed Username field landed.
		if u, v, ok := strings.Cut(value, ":"); ok {
			username, value = u, v
		}
	}
	return &Secret{
		SecretMeta: SecretMeta{
			ID:        sf.ID,
			Name:      sf.Name,
			Type:      sf.Type,
			Scope:     sf.Scope,
			CreatedAt: sf.CreatedAt,
		},
		Username: username,
		Value:    value,
	}, nil
}
