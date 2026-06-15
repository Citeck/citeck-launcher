package storage

import (
	"strings"
	"time"
)

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
// is one of "NONE" or "TOKEN".
//
// SecretID references a REUSABLE secret by its id (e.g. one GitLab token
// shared across several customer workspaces). When set, the daemon resolves
// the repo auth token from that secret; when empty, TOKEN auth falls back to
// the legacy per-workspace secret under key "ws:{ID}:repo" (Kotlin
// getRepoAuthId convention). Shared secrets are never auto-deleted when a
// referencing workspace is deleted.
type WorkspaceDto struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	RepoBranch     string `json:"repoBranch"`
	RepoPullPeriod string `json:"repoPullPeriod,omitempty"`
	AuthType       string `json:"authType,omitempty"`
	SecretID       string `json:"secretId,omitempty"`
}

// SecretMeta holds non-sensitive secret metadata.
//
// Username (BASIC_AUTH / REGISTRY_AUTH only) lives here — not on Secret —
// because it is metadata, not a credential: the write-only secret-edit form
// prefills it from the meta listing without ever decrypting the value.
type SecretMeta struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Type  SecretType `json:"type"`
	Scope string     `json:"scope"`
	// Host is the registry/git host this secret authenticates against (e.g.
	// "enterprise-registry.citeck.ru"). Used by the host-filtered secret
	// picker so the user reuses an existing credential instead of re-entering
	// it per workspace/namespace. Backfilled for legacy REGISTRY_AUTH secrets
	// from their "images-repo:<host>" scope. Empty for host-agnostic secrets.
	Host      string    `json:"host,omitempty"`
	Username  string    `json:"username,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Secret holds a full secret including its value.
//
// For BASIC_AUTH / REGISTRY_AUTH, the (promoted) Username carries the user
// part and Value carries the password verbatim — passwords containing ':'
// must round-trip untouched (PATs, generated creds). The legacy "user:pass"
// packing is auto-split on load (FileStore.readSecret, SQLite migration v3).
type Secret struct {
	SecretMeta
	Value string `json:"-"` // never serialized in API responses
}

// Credentials returns the (user, pass) pair carried by an auth secret.
// The typed Username field wins; when it is empty, a legacy "user:pass"
// packed Value is split as a last-resort fallback — for any secret that
// somehow survived the FileStore / SQLite-v3 migration paths without a
// Username column populated. ok is false when no usable pair can be derived
// (no colon to split on, or an empty user/password half).
func (s *Secret) Credentials() (user, pass string, ok bool) {
	user, pass = s.Username, s.Value
	if user == "" {
		var cut bool
		user, pass, cut = strings.Cut(s.Value, ":")
		if !cut {
			return "", "", false
		}
	}
	if user == "" || pass == "" {
		return "", "", false
	}
	return user, pass, true
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

// NamespaceRow is namespace listing metadata — enough to render the list
// without parsing the config/state blobs.
type NamespaceRow struct {
	ID     string
	Name   string
	Status string
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

	// Registry auth bindings: per-workspace map of image-registry host →
	// secret id, so one stored REGISTRY_AUTH credential is reused across
	// namespaces (and, in desktop mode, workspaces) instead of being
	// re-entered per host. SetRegistryBinding with an empty secretID removes
	// the binding. Server mode has a single implicit workspace (wsID ignored).
	ListRegistryBindings(wsID string) (map[string]string, error)
	SetRegistryBinding(wsID, host, secretID string) error

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

	// Namespaces (config + per-NS runtime state). Opaque strings keep this
	// interface free of internal/namespace (no import cycle). Desktop:
	// SQLite rows. Server: mapped to conf/namespace.yml + runtime state file.
	ListNamespaces(wsID string) ([]NamespaceRow, error)
	LoadNamespaceConfig(wsID, nsID string) (configYAML string, ok bool, err error)
	SaveNamespaceConfig(wsID, nsID, name, configYAML string) error
	LoadNamespaceState(wsID, nsID string) (stateJSON string, ok bool, err error)
	SaveNamespaceState(wsID, nsID, status, stateJSON string) error
	DeleteNamespace(wsID, nsID string) error

	// Close releases resources (e.g., database connections).
	Close() error
}
