package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestSQLiteMigrationV7_AddsWorkspaceSecretID verifies the v6 → v7 upgrade
// path: an existing workspaces table gains the secret_id column with an empty
// default, and pre-existing rows read back with SecretID == "".
func TestSQLiteMigrationV7_AddsWorkspaceSecretID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "launcher.db")

	// Seed a v6 schema by hand so migrate() picks up at v7. Only the tables
	// the v7 migration + subsequent assertions touch are needed.
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	mustExec := func(q string, args ...any) {
		t.Helper()
		_, execErr := db.Exec(q, args...)
		require.NoError(t, execErr, "exec %q", q)
	}
	mustExec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	mustExec(`INSERT INTO schema_version VALUES (6)`)
	mustExec(`CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		repo_url TEXT NOT NULL DEFAULT '',
		repo_branch TEXT NOT NULL DEFAULT 'main',
		repo_pull_period TEXT NOT NULL DEFAULT 'PT2H',
		auth_type TEXT NOT NULL DEFAULT 'NONE'
	)`)
	mustExec(`INSERT INTO workspaces (id, name, repo_url, repo_branch, repo_pull_period, auth_type)
		VALUES ('ws-old', 'Old', 'https://gitlab.example.com/old.git', 'main', 'PT2H', 'TOKEN')`)
	mustExec(`CREATE TABLE launcher_state (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`)
	// A real v6 DB always has the secrets table (created at v1, username added
	// at v3); seed it so the later v8 migration (ADD COLUMN host) can run.
	mustExec(`CREATE TABLE secrets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'global',
		username TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	)`)
	require.NoError(t, db.Close())

	store, err := NewSQLiteStore(dir)
	require.NoError(t, err)
	defer store.Close()

	var v int
	require.NoError(t, store.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v))
	require.GreaterOrEqual(t, v, 7)

	// Pre-existing row survives with an empty SecretID (legacy ws:{id}:repo
	// token resolution keeps working for it).
	ws, err := store.GetWorkspace("ws-old")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "TOKEN", ws.AuthType)
	assert.Empty(t, ws.SecretID)

	// The migrated row can be re-saved with a SecretID and round-trips.
	ws.SecretID = "shared-gitlab-token"
	require.NoError(t, store.SaveWorkspace(*ws))
	got, err := store.GetWorkspace("ws-old")
	require.NoError(t, err)
	assert.Equal(t, "shared-gitlab-token", got.SecretID)
}

// TestSQLiteWorkspaceSecretIDRoundTrip pins the secretId persistence contract
// on the desktop store: save → get → list → unlink (save with "").
func TestSQLiteWorkspaceSecretIDRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.SaveWorkspace(WorkspaceDto{
		ID:       "ws-a",
		Name:     "A",
		RepoURL:  "https://gitlab.example.com/a.git",
		AuthType: "TOKEN",
		SecretID: "shared-token",
	}))

	got, err := store.GetWorkspace("ws-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "shared-token", got.SecretID)

	list, err := store.ListWorkspaces()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "shared-token", list[0].SecretID)

	// Unlink: empty SecretID persists as empty (not "keep previous").
	got.SecretID = ""
	require.NoError(t, store.SaveWorkspace(*got))
	unlinked, err := store.GetWorkspace("ws-a")
	require.NoError(t, err)
	assert.Empty(t, unlinked.SecretID)
}

// TestFileStoreWorkspaceSecretID_NoOp documents the server-mode contract:
// FileStore has a single implicit workspace and never persists workspace rows,
// so the additive SecretID field needs no FileStore storage change — Save is a
// no-op and the implicit workspace carries no secret reference.
func TestFileStoreWorkspaceSecretID_NoOp(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, filepath.Join(dir, "runtime"))
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.SaveWorkspace(WorkspaceDto{ID: "daemon", SecretID: "shared-token"}))
	ws, err := store.GetWorkspace("daemon")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Empty(t, ws.SecretID, "server-mode implicit workspace never carries a secret reference")
}
