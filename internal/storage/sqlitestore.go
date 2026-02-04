package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// SQLiteStore implements Store using a SQLite database. Used in desktop mode.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates a SQLite database at the given directory.
// The database file is named "launcher.db".
func NewSQLiteStore(dir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G301: user home dir needs group access
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	dbPath := filepath.Join(dir, "launcher.db")
	// Remove stale WAL/SHM files from unclean shutdown — prevents SQLITE_IOERR_SHORT_READ
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Serialize all database access — correct for single-tenant desktop app,
	// prevents concurrent write deadlocks with pure-Go SQLite driver.
	db.SetMaxOpenConns(1)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close() // best-effort cleanup on migration failure
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	// Restrict DB file permissions — contains secrets in desktop mode.
	// Must be after migrate() since sql.Open is lazy and the file is created on first query.
	if err := os.Chmod(dbPath, 0o600); err != nil {
		_ = db.Close() // best-effort cleanup on chmod failure
		return nil, fmt.Errorf("chmod db file: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	// Ensure schema_version table exists
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	var currentVersion int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&currentVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read schema_version: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		currentVersion = 0
		if _, err := s.db.Exec("INSERT INTO schema_version (version) VALUES (0)"); err != nil {
			return fmt.Errorf("insert initial schema version: %w", err)
		}
	}

	// Sequential migrations
	migrations := []struct {
		version int
		stmts   []string
	}{
		{
			version: 1,
			stmts: []string{
				`CREATE TABLE IF NOT EXISTS workspaces (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL DEFAULT '',
					repo_url TEXT NOT NULL DEFAULT '',
					repo_branch TEXT NOT NULL DEFAULT 'main'
				)`,
				`CREATE TABLE IF NOT EXISTS secrets (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL DEFAULT '',
					type TEXT NOT NULL DEFAULT '',
					value TEXT NOT NULL DEFAULT '',
					scope TEXT NOT NULL DEFAULT 'global',
					created_at TEXT NOT NULL DEFAULT ''
				)`,
				`CREATE TABLE IF NOT EXISTS launcher_state (
					key TEXT PRIMARY KEY,
					value TEXT NOT NULL DEFAULT ''
				)`,
			},
		},
		// Future migrations go here:
		// { version: 2, stmts: []string{`ALTER TABLE ...`} },
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}
		if err := s.applyMigration(m.version, m.stmts); err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLiteStore) applyMigration(version int, stmts []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration v%d: %w", version, err)
	}
	defer tx.Rollback()

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration v%d: %w", version, err)
		}
	}
	if _, err := tx.Exec("UPDATE schema_version SET version = ?", version); err != nil {
		return fmt.Errorf("update schema_version to v%d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration v%d: %w", version, err)
	}
	return nil
}

// --- Workspaces ---

// ListWorkspaces returns all workspaces ordered by ID.
func (s *SQLiteStore) ListWorkspaces() ([]WorkspaceDto, error) {
	rows, err := s.db.Query("SELECT id, name, repo_url, repo_branch FROM workspaces ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query workspaces: %w", err)
	}
	defer rows.Close()

	var result []WorkspaceDto
	for rows.Next() {
		var ws WorkspaceDto
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch); err != nil {
			return nil, fmt.Errorf("scan workspace row: %w", err)
		}
		result = append(result, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return result, nil
}

// GetWorkspace returns a single workspace by ID.
func (s *SQLiteStore) GetWorkspace(id string) (*WorkspaceDto, error) {
	var ws WorkspaceDto
	err := s.db.QueryRow(
		"SELECT id, name, repo_url, repo_branch FROM workspaces WHERE id = ?", id,
	).Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workspace %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query workspace %s: %w", id, err)
	}
	return &ws, nil
}

// SaveWorkspace inserts or updates a workspace (upsert).
func (s *SQLiteStore) SaveWorkspace(ws WorkspaceDto) error {
	_, err := s.db.Exec(`
		INSERT INTO workspaces (id, name, repo_url, repo_branch) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, repo_url=excluded.repo_url, repo_branch=excluded.repo_branch
	`, ws.ID, ws.Name, ws.RepoURL, ws.RepoBranch)
	if err != nil {
		return fmt.Errorf("save workspace %s: %w", ws.ID, err)
	}
	return nil
}

// DeleteWorkspace removes a workspace by ID.
func (s *SQLiteStore) DeleteWorkspace(id string) error {
	if _, err := s.db.Exec("DELETE FROM workspaces WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete workspace %s: %w", id, err)
	}
	return nil
}

// --- Secrets ---

// ListSecrets returns metadata for all secrets (without values).
func (s *SQLiteStore) ListSecrets() ([]SecretMeta, error) {
	rows, err := s.db.Query("SELECT id, name, type, scope, created_at FROM secrets ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query secrets: %w", err)
	}
	defer rows.Close()

	var result []SecretMeta
	for rows.Next() {
		var sm SecretMeta
		var createdStr string
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.Type, &sm.Scope, &createdStr); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}
		sm.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		result = append(result, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate secrets: %w", err)
	}
	return result, nil
}

// GetSecret returns a secret including its value.
func (s *SQLiteStore) GetSecret(id string) (*Secret, error) {
	var sec Secret
	var createdStr string
	err := s.db.QueryRow(
		"SELECT id, name, type, value, scope, created_at FROM secrets WHERE id = ?", id,
	).Scan(&sec.ID, &sec.Name, &sec.Type, &sec.Value, &sec.Scope, &createdStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("secret %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query secret %s: %w", id, err)
	}
	sec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	return &sec, nil
}

// SaveSecret inserts or updates a secret (upsert).
func (s *SQLiteStore) SaveSecret(secret Secret) error {
	if secret.Scope == "" {
		secret.Scope = "global"
	}
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`
		INSERT INTO secrets (id, name, type, value, scope, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, value=excluded.value, scope=excluded.scope
	`, secret.ID, secret.Name, secret.Type, secret.Value, secret.Scope, secret.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save secret %s: %w", secret.ID, err)
	}
	return nil
}

// DeleteSecret removes a secret by ID.
func (s *SQLiteStore) DeleteSecret(id string) error {
	if _, err := s.db.Exec("DELETE FROM secrets WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete secret %s: %w", id, err)
	}
	return nil
}

// --- Launcher State ---

// GetState returns the persisted launcher state.
func (s *SQLiteStore) GetState() (*LauncherState, error) {
	state := &LauncherState{}
	_ = s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'workspace_id'").Scan(&state.WorkspaceID)
	_ = s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'namespace_id'").Scan(&state.NamespaceID)
	return state, nil
}

// SetState persists the launcher state (workspace and namespace selection).
func (s *SQLiteStore) SetState(state LauncherState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin state tx: %w", err)
	}
	defer tx.Rollback()

	stmt := `INSERT INTO launcher_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	if _, err := tx.Exec(stmt, "workspace_id", state.WorkspaceID); err != nil {
		return fmt.Errorf("set workspace_id: %w", err)
	}
	if _, err := tx.Exec(stmt, "namespace_id", state.NamespaceID); err != nil {
		return fmt.Errorf("set namespace_id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

// PutSecretBlob stores the encrypted secrets blob (migrated from Kotlin launcher).
func (s *SQLiteStore) PutSecretBlob(base64Data string) error {
	stmt := `INSERT INTO launcher_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	if _, err := s.db.Exec(stmt, "secret_blob", base64Data); err != nil {
		return fmt.Errorf("put secret blob: %w", err)
	}
	return nil
}

// GetSecretBlob retrieves the encrypted secrets blob.
func (s *SQLiteStore) GetSecretBlob() (string, error) {
	var val string
	if err := s.db.QueryRow(`SELECT value FROM launcher_state WHERE key = ?`, "secret_blob").Scan(&val); err != nil {
		return "", fmt.Errorf("get secret blob: %w", err)
	}
	return val, nil
}

// GetStateValue reads a single key from launcher_state. Returns "" if not found.
func (s *SQLiteStore) GetStateValue(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM launcher_state WHERE key = ?", key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get state %s: %w", key, err)
	}
	return val, nil
}

// SetStateValue writes a single key to launcher_state (upsert).
// Empty value deletes the key (consistent with FileStore).
func (s *SQLiteStore) SetStateValue(key, value string) error {
	if value == "" {
		if _, err := s.db.Exec("DELETE FROM launcher_state WHERE key = ?", key); err != nil {
			return fmt.Errorf("delete state %s: %w", key, err)
		}
		return nil
	}
	stmt := `INSERT INTO launcher_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	if _, err := s.db.Exec(stmt, key, value); err != nil {
		return fmt.Errorf("set state %s: %w", key, err)
	}
	return nil
}

// DB returns the underlying *sql.DB for transactional operations.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}
