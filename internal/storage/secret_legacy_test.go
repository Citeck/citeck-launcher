package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSQLiteMigrationV3_SplitsLegacyUserPass verifies that an existing v2
// database with a BASIC_AUTH / REGISTRY_AUTH secret stored as "user:pass" in
// the value column is split into the new username column on first v3 upgrade.
// A GIT_TOKEN row containing ':' must NOT be touched (PATs can contain ':').
func TestSQLiteMigrationV3_SplitsLegacyUserPass(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "launcher.db")

	// Build a v2 database by hand: schema_version=2, secrets table without
	// the username column, three rows seeded.
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
	mustExec(`INSERT INTO schema_version VALUES (2)`)
	mustExec(`CREATE TABLE secrets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'global',
		created_at TEXT NOT NULL DEFAULT ''
	)`)
	mustExec(`CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		repo_url TEXT NOT NULL DEFAULT '',
		repo_branch TEXT NOT NULL DEFAULT 'main',
		repo_pull_period TEXT NOT NULL DEFAULT 'PT2H',
		auth_type TEXT NOT NULL DEFAULT 'NONE'
	)`)
	mustExec(`CREATE TABLE launcher_state (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`)
	mustExec(`INSERT INTO secrets (id, name, type, value) VALUES (?, ?, ?, ?)`,
		"reg-1", "registry creds", "REGISTRY_AUTH", "alice:pa:ss:wo:rd")
	mustExec(`INSERT INTO secrets (id, name, type, value) VALUES (?, ?, ?, ?)`,
		"basic-1", "basic creds", "BASIC_AUTH", "bob:secret")
	// GIT_TOKEN value with ':' — must remain untouched.
	mustExec(`INSERT INTO secrets (id, name, type, value) VALUES (?, ?, ?, ?)`,
		"git-1", "git token", "GIT_TOKEN", "glpat-abc:xyz")
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close v2 db: %v", closeErr)
	}

	// Open via NewSQLiteStore — migration v3 must run.
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	reg, err := store.GetSecret("reg-1")
	if err != nil {
		t.Fatalf("GetSecret(reg-1): %v", err)
	}
	if reg.Username != "alice" {
		t.Errorf("reg-1 Username = %q, want 'alice'", reg.Username)
	}
	if reg.Value != "pa:ss:wo:rd" {
		t.Errorf("reg-1 Value = %q, want 'pa:ss:wo:rd' (full password preserved)", reg.Value)
	}

	basic, err := store.GetSecret("basic-1")
	if err != nil {
		t.Fatalf("GetSecret(basic-1): %v", err)
	}
	if basic.Username != "bob" || basic.Value != "secret" {
		t.Errorf("basic-1 = (%q,%q), want ('bob','secret')", basic.Username, basic.Value)
	}

	git, err := store.GetSecret("git-1")
	if err != nil {
		t.Fatalf("GetSecret(git-1): %v", err)
	}
	if git.Username != "" {
		t.Errorf("git-1 Username = %q, GIT_TOKEN must NOT be split", git.Username)
	}
	if git.Value != "glpat-abc:xyz" {
		t.Errorf("git-1 Value = %q, want full PAT preserved", git.Value)
	}
}

// TestFileStore_LegacyUserPassSplitOnLoad covers on-disk JSON secrets written
// before BASIC_AUTH gained a typed Username field. The split must fire only
// for BASIC_AUTH / REGISTRY_AUTH and must preserve passwords containing ':'.
func TestFileStore_LegacyUserPassSplitOnLoad(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	legacy := `{
  "id": "reg-1",
  "name": "Registry",
  "type": "REGISTRY_AUTH",
  "value": "alice:pa:ss:word",
  "scope": "registry.example.com",
  "createdAt": "2025-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(secretsDir, "reg-1.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy secret: %v", err)
	}

	store, err := NewFileStore(dir, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer store.Close()

	got, err := store.GetSecret("reg-1")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q, want 'alice'", got.Username)
	}
	if got.Value != "pa:ss:word" {
		t.Errorf("Value = %q, want 'pa:ss:word'", got.Value)
	}
}
