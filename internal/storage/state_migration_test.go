package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSQLiteMigrationV4_FoldsLegacyNamespaceID verifies the v3 → v4 upgrade
// path: the old global namespace_id key is folded into the new selected_ns
// JSON keyed by workspace_id, then the legacy row is dropped.
func TestSQLiteMigrationV4_FoldsLegacyNamespaceID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "launcher.db")

	// Seed a v3 schema by hand so migrate() picks up at v4.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, execErr := db.Exec(q, args...); execErr != nil {
			t.Fatalf("exec %q: %v", q, execErr)
		}
	}
	mustExec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	mustExec(`INSERT INTO schema_version VALUES (3)`)
	mustExec(`CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		repo_url TEXT NOT NULL DEFAULT '',
		repo_branch TEXT NOT NULL DEFAULT 'main',
		repo_pull_period TEXT NOT NULL DEFAULT 'PT2H',
		auth_type TEXT NOT NULL DEFAULT 'NONE'
	)`)
	mustExec(`CREATE TABLE secrets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL DEFAULT '',
		username TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'global',
		created_at TEXT NOT NULL DEFAULT ''
	)`)
	mustExec(`CREATE TABLE launcher_state (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`)
	mustExec(`INSERT INTO launcher_state (key, value) VALUES ('workspace_id', 'foo')`)
	mustExec(`INSERT INTO launcher_state (key, value) VALUES ('namespace_id', 'bar')`)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close v3 db: %v", closeErr)
	}

	// Open via NewSQLiteStore → triggers migrate(), v4 should fold the row.
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	state, err := store.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.WorkspaceID != "foo" {
		t.Errorf("WorkspaceID = %q, want 'foo'", state.WorkspaceID)
	}
	if got := state.NamespaceID(); got != "bar" {
		t.Errorf("NamespaceID() = %q, want 'bar'", got)
	}
	if state.SelectedNs["foo"] != "bar" {
		t.Errorf("SelectedNs[foo] = %q, want 'bar'", state.SelectedNs["foo"])
	}

	// Legacy namespace_id key must be gone — otherwise round-tripping SetState
	// would re-read a stale value on next startup.
	var legacy string
	err = store.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'namespace_id'").Scan(&legacy)
	if err == nil {
		t.Errorf("legacy namespace_id row still present: %q", legacy)
	}
}

// TestSwitchWorkspaceRoundTrip exercises the per-workspace selection contract
// directly at the storage layer (no daemon): set ns1 in workspace A, switch to
// B and set ns2, switch back to A — ns1 must still be the selection. This is
// what Kotlin's workspace-state/{wsId} → SELECTED_NS_PROP guarantees.
func TestSwitchWorkspaceRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	mustSet := func(ws, ns string, all map[string]string) {
		t.Helper()
		if err := store.SetState(LauncherState{WorkspaceID: ws, SelectedNs: all}); err != nil {
			t.Fatalf("SetState(%s/%s): %v", ws, ns, err)
		}
	}

	// Workspace A → ns1
	mustSet("A", "ns1", map[string]string{"A": "ns1"})

	// Switch to B → ns2 (preserve A's selection)
	mustSet("B", "ns2", map[string]string{"A": "ns1", "B": "ns2"})

	// Switch back to A — ns1 must still resolve
	mustSet("A", "ns1", map[string]string{"A": "ns1", "B": "ns2"})

	state, err := store.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.WorkspaceID != "A" {
		t.Errorf("WorkspaceID = %q, want 'A'", state.WorkspaceID)
	}
	if got := state.NamespaceID(); got != "ns1" {
		t.Errorf("NamespaceID() = %q, want 'ns1'", got)
	}
	if state.SelectedNs["B"] != "ns2" {
		t.Errorf("B's selection lost: SelectedNs[B] = %q", state.SelectedNs["B"])
	}
}
