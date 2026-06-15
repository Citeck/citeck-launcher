package storage

import (
	"path/filepath"
	"testing"
	"time"
)

// testStoreWorkspaces runs workspace tests (only for stores that support real workspace CRUD).
func testStoreWorkspaces(t *testing.T, store Store) {
	t.Helper()

	// Save
	ws := WorkspaceDto{ID: "ws1", Name: "Test Workspace", RepoURL: "https://github.com/test/repo.git", RepoBranch: "main"}
	if err := store.SaveWorkspace(ws); err != nil {
		t.Fatalf("SaveWorkspace() error: %v", err)
	}

	// Get
	got, err := store.GetWorkspace("ws1")
	if err != nil {
		t.Fatalf("GetWorkspace() error: %v", err)
	}
	if got.Name != "Test Workspace" {
		t.Errorf("Name = %q, want 'Test Workspace'", got.Name)
	}
	if got.RepoURL != "https://github.com/test/repo.git" {
		t.Errorf("RepoURL = %q, want git URL", got.RepoURL)
	}

	// List
	list, err := store.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces() error: %v", err)
	}
	if len(list) < 1 {
		t.Error("ListWorkspaces() should return at least 1 workspace")
	}

	// Update (upsert)
	ws.Name = "Updated"
	if err := store.SaveWorkspace(ws); err != nil {
		t.Fatalf("SaveWorkspace(update) error: %v", err)
	}
	got, _ = store.GetWorkspace("ws1")
	if got.Name != "Updated" {
		t.Errorf("After update, Name = %q, want 'Updated'", got.Name)
	}

	// Delete
	if err := store.DeleteWorkspace("ws1"); err != nil {
		t.Fatalf("DeleteWorkspace() error: %v", err)
	}
}

// testStoreSecrets runs the secret test suite against any Store implementation.
func testStoreSecrets(t *testing.T, store Store) {
	t.Helper()

	// Save
	sec := Secret{
		SecretMeta: SecretMeta{
			ID:        "git-token",
			Name:      "GitLab Token",
			Type:      SecretGitToken,
			Scope:     "global",
			CreatedAt: time.Now().Truncate(time.Second),
		},
		Value: "glpat-xxxx",
	}
	if err := store.SaveSecret(sec); err != nil {
		t.Fatalf("SaveSecret() error: %v", err)
	}

	// Get (with value)
	got, err := store.GetSecret("git-token")
	if err != nil {
		t.Fatalf("GetSecret() error: %v", err)
	}
	if got.Value != "glpat-xxxx" {
		t.Errorf("Value = %q, want 'glpat-xxxx'", got.Value)
	}
	if got.Type != SecretGitToken {
		t.Errorf("Type = %q, want %q", got.Type, SecretGitToken)
	}

	// List (no values)
	list, err := store.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets() error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListSecrets() = %d items, want 1", len(list))
	}
	if list[0].Name != "GitLab Token" {
		t.Errorf("Listed Name = %q, want 'GitLab Token'", list[0].Name)
	}

	// Delete
	err = store.DeleteSecret("git-token")
	if err != nil {
		t.Fatalf("DeleteSecret() error: %v", err)
	}
	list, _ = store.ListSecrets()
	if len(list) != 0 {
		t.Errorf("After delete, ListSecrets() = %d items, want 0", len(list))
	}

	// BASIC_AUTH with typed Username + password containing ':' — round-trip
	// (Kotlin AuthSecret.Basic parity). Splitting on ':' would truncate the
	// password; the storage layer must preserve both fields verbatim.
	basic := Secret{
		SecretMeta: SecretMeta{
			ID:       "basic-1",
			Name:     "Registry creds",
			Type:     SecretRegistryAuth,
			Scope:    "registry.example.com",
			Username: "alice",
		},
		Value: "pa:ss:wo:rd",
	}
	err = store.SaveSecret(basic)
	if err != nil {
		t.Fatalf("SaveSecret(basic) error: %v", err)
	}
	got2, err := store.GetSecret("basic-1")
	if err != nil {
		t.Fatalf("GetSecret(basic) error: %v", err)
	}
	if got2.Username != "alice" {
		t.Errorf("Username = %q, want 'alice'", got2.Username)
	}
	if got2.Value != "pa:ss:wo:rd" {
		t.Errorf("Value = %q, want 'pa:ss:wo:rd'", got2.Value)
	}
	_ = store.DeleteSecret("basic-1")
}

// testStoreState runs state persistence tests against any Store implementation.
func testStoreState(t *testing.T, store Store) {
	t.Helper()

	// Initial state should be empty
	state, err := store.GetState()
	if err != nil {
		t.Fatalf("GetState() error: %v", err)
	}
	if state.WorkspaceID != "" || state.NamespaceID() != "" {
		t.Errorf("Initial state should be empty, got %+v", state)
	}

	// Set state
	if setErr := store.SetState(LauncherState{
		WorkspaceID: "ws1",
		SelectedNs:  map[string]string{"ws1": "ns1"},
	}); setErr != nil {
		t.Fatalf("SetState() error: %v", setErr)
	}

	// Read back
	state, err = store.GetState()
	if err != nil {
		t.Fatalf("GetState() after set error: %v", err)
	}
	if state.WorkspaceID != "ws1" {
		t.Errorf("WorkspaceID = %q, want 'ws1'", state.WorkspaceID)
	}
	if state.NamespaceID() != "ns1" {
		t.Errorf("NamespaceID() = %q, want 'ns1'", state.NamespaceID())
	}

	// Update state — switch to ws2 keeping the ws1 entry to verify per-workspace
	// preservation (the key invariant introduced by 11c).
	if err := store.SetState(LauncherState{
		WorkspaceID: "ws2",
		SelectedNs:  map[string]string{"ws1": "ns1", "ws2": "ns2"},
	}); err != nil {
		t.Fatalf("SetState(update) error: %v", err)
	}
	state, _ = store.GetState()
	if state.WorkspaceID != "ws2" || state.NamespaceID() != "ns2" {
		t.Errorf("After update, state = %+v, want ws2/ns2", state)
	}
	if state.SelectedNs["ws1"] != "ns1" {
		t.Errorf("ws1 selection lost on switch, got %q", state.SelectedNs["ws1"])
	}
}

func TestFileStore(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewFileStore() error: %v", err)
	}
	defer store.Close()

	t.Run("Workspaces", func(t *testing.T) {
		// FileStore returns a single implicit "daemon" workspace
		list, err := store.ListWorkspaces()
		if err != nil {
			t.Fatalf("ListWorkspaces() error: %v", err)
		}
		if len(list) != 1 || list[0].ID != "daemon" {
			t.Errorf("FileStore should return single 'daemon' workspace, got %v", list)
		}
	})

	t.Run("Secrets", func(t *testing.T) {
		testStoreSecrets(t, store)
	})

	t.Run("State", func(t *testing.T) {
		testStoreState(t, store)
	})

}

func TestSQLiteStore(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore() error: %v", err)
	}
	defer store.Close()

	t.Run("Workspaces", func(t *testing.T) {
		testStoreWorkspaces(t, store)
	})

	t.Run("Secrets", func(t *testing.T) {
		testStoreSecrets(t, store)
	})

	t.Run("State", func(t *testing.T) {
		testStoreState(t, store)
	})

}
