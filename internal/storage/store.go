package storage

import "time"

// SecretType identifies the kind of secret.
type SecretType string

const (
	SecretGitToken    SecretType = "GIT_TOKEN"
	SecretBasicAuth   SecretType = "BASIC_AUTH"
	SecretRegistryAuth SecretType = "REGISTRY_AUTH"
)

// WorkspaceDto represents a workspace record.
type WorkspaceDto struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RepoURL    string `json:"repoUrl"`
	RepoBranch string `json:"repoBranch"`
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
type Secret struct {
	SecretMeta
	Value string `json:"-"` // never serialized in API responses
}

// LauncherState holds persisted launcher state (selected workspace/namespace).
type LauncherState struct {
	WorkspaceID string `json:"workspaceId"`
	NamespaceID string `json:"namespaceId"`
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

	// Launcher state
	GetState() (*LauncherState, error)
	SetState(state LauncherState) error

	// Close releases resources (e.g., database connections).
	Close() error
}
