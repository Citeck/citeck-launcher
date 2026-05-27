package storage

import "time"

// SecretType identifies the kind of secret.
type SecretType string

const (
	// SecretGitToken identifies a Git access token secret.
	SecretGitToken SecretType = "GIT_TOKEN" //nolint:gosec // G101: constant name, not a credential
	// SecretBasicAuth identifies a basic-auth credential secret.
	SecretBasicAuth SecretType = "BASIC_AUTH"
	// SecretRegistryAuth identifies a Docker registry credential secret.
	SecretRegistryAuth SecretType = "REGISTRY_AUTH" //nolint:gosec // G101: constant name, not a credential
	// SecretSystem identifies a system-managed secret (JWT, OIDC).
	SecretSystem SecretType = "SYSTEM" //nolint:gosec // G101: constant name, not a credential
)

// WorkspaceDto represents a workspace record.
//
// RepoPullPeriod is an ISO 8601 duration string (e.g. "PT2H" for 2 hours) — same
// wire format as Kotlin's WorkspaceDto for round-trip compatibility with v1.x
// data; parsed via ParseISO8601Duration when applied to git.RepoOpts. AuthType
// is one of "NONE" or "TOKEN"; TOKEN means the daemon should resolve the
// secret stored under key "ws:{ID}:repo" (Kotlin getRepoAuthId convention).
type WorkspaceDto struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
}

// SecretMeta holds non-sensitive secret metadata.
type SecretMeta struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      SecretType `json:"type"`
	Scope     string     `json:"scope"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Secret holds a full secret including its value.
//
// For BASIC_AUTH / REGISTRY_AUTH, Username carries the user part and Value
// carries the password verbatim — passwords containing ':' must round-trip
// untouched (PATs, generated creds). The legacy "user:pass" packing is
// auto-split on load (FileStore.readSecret, SQLite migration v3).
type Secret struct {
	SecretMeta
	Username string `json:"username,omitempty"`
	Value    string `json:"-"` // never serialized in API responses
}

// GitRepoState holds per-repo sync metadata persisted across launcher restarts.
//
// Migrated from Kotlin's `git-repo!instances` map (GitRepoService.kt). The
// repoProps half of Kotlin's GitRepoInstance is dropped — Go derives URL/branch
// from workspace config + bundle YAML on every sync, so re-storing them would
// invite drift. Only the throttle/cache fields are persisted:
//
//   - Path: relative repo path under the launcher home (e.g.
//     "ws/{wsID}/bundles/{bundleID}") — same key Kotlin used so a v1 → v2
//     migration round-trips without re-pulling every repo on first boot.
//   - LastSyncMs: Unix millis of the most recent successful sync; replaces
//     git.lastSyncTimes for restart-survivable throttling.
//   - LastCommitHash: HEAD hash captured at last sync; used by the Kotlin
//     no-op heuristic (skip pull if remote HEAD already matches). Kept here
//     for parity even though the current Go pull path doesn't yet consult it.
type GitRepoState struct {
	Path           string `json:"path"`
	LastSyncMs     int64  `json:"lastSyncMs"`
	LastCommitHash string `json:"lastCommitHash,omitempty"`
}

// LauncherState holds persisted launcher state.
//
// SelectedNs is the per-workspace namespace selection (Kotlin parity:
// workspace-state/{wsId} → SELECTED_NS_PROP). Switching workspaces preserves
// the previous workspace's selection so re-activation restores it instead of
// dropping the user back on Welcome.
type LauncherState struct {
	WorkspaceID string            `json:"workspaceId"`
	SelectedNs  map[string]string `json:"selectedNs,omitempty"`
}

// NamespaceID returns the selected namespace for the current workspace, or ""
// when no selection is recorded. Convenience accessor for the common case.
func (s *LauncherState) NamespaceID() string {
	if s == nil || s.SelectedNs == nil {
		return ""
	}
	return s.SelectedNs[s.WorkspaceID]
}

// Store defines the storage abstraction used by both server and desktop modes.
type Store interface {
	// Workspaces
	ListWorkspaces() ([]WorkspaceDto, error)
	GetWorkspace(id string) (*WorkspaceDto, error)
	SaveWorkspace(ws WorkspaceDto) error
	DeleteWorkspace(id string) error

	// Secrets
	ListSecrets() ([]SecretMeta, error)
	GetSecret(id string) (*Secret, error)
	SaveSecret(secret Secret) error
	DeleteSecret(id string) error

	// Encrypted secrets blob (migrated from Kotlin launcher)
	PutSecretBlob(base64Data string) error
	GetSecretBlob() (string, error)

	// Launcher state
	GetState() (*LauncherState, error)
	SetState(state LauncherState) error

	// Key-value state (used by SecretService for encryption metadata)
	GetStateValue(key string) (string, error)
	SetStateValue(key, value string) error

	// Git repo sync metadata (migrated from Kotlin's git-repo!instances).
	GetGitRepoState(path string) (*GitRepoState, error)
	SetGitRepoState(state GitRepoState) error
	ListGitRepoStates() ([]GitRepoState, error)

	// Close releases resources (e.g., database connections).
	Close() error
}
