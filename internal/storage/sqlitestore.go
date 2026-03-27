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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	dbPath := filepath.Join(dir, "launcher.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Serialize all database access — correct for single-tenant desktop app,
	// prevents concurrent write deadlocks with pure-Go SQLite driver.
	db.SetMaxOpenConns(1)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	stmts := []string{
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// --- Workspaces ---

func (s *SQLiteStore) ListWorkspaces() ([]WorkspaceDto, error) {
	rows, err := s.db.Query("SELECT id, name, repo_url, repo_branch FROM workspaces ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WorkspaceDto
	for rows.Next() {
		var ws WorkspaceDto
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch); err != nil {
			return nil, err
		}
		result = append(result, ws)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) GetWorkspace(id string) (*WorkspaceDto, error) {
	var ws WorkspaceDto
	err := s.db.QueryRow(
		"SELECT id, name, repo_url, repo_branch FROM workspaces WHERE id = ?", id,
	).Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workspace %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func (s *SQLiteStore) SaveWorkspace(ws WorkspaceDto) error {
	_, err := s.db.Exec(`
		INSERT INTO workspaces (id, name, repo_url, repo_branch) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, repo_url=excluded.repo_url, repo_branch=excluded.repo_branch
	`, ws.ID, ws.Name, ws.RepoURL, ws.RepoBranch)
	return err
}

func (s *SQLiteStore) DeleteWorkspace(id string) error {
	_, err := s.db.Exec("DELETE FROM workspaces WHERE id = ?", id)
	return err
}

// --- Secrets ---

func (s *SQLiteStore) ListSecrets() ([]SecretMeta, error) {
	rows, err := s.db.Query("SELECT id, name, type, scope, created_at FROM secrets ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SecretMeta
	for rows.Next() {
		var sm SecretMeta
		var createdStr string
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.Type, &sm.Scope, &createdStr); err != nil {
			return nil, err
		}
		sm.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		result = append(result, sm)
	}
	return result, rows.Err()
}

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
		return nil, err
	}
	sec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	return &sec, nil
}

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
	return err
}

func (s *SQLiteStore) DeleteSecret(id string) error {
	_, err := s.db.Exec("DELETE FROM secrets WHERE id = ?", id)
	return err
}

// --- Launcher State ---

func (s *SQLiteStore) GetState() (*LauncherState, error) {
	state := &LauncherState{}
	_ = s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'workspace_id'").Scan(&state.WorkspaceID)
	_ = s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'namespace_id'").Scan(&state.NamespaceID)
	return state, nil
}

func (s *SQLiteStore) SetState(state LauncherState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt := `INSERT INTO launcher_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	if _, err := tx.Exec(stmt, "workspace_id", state.WorkspaceID); err != nil {
		return err
	}
	if _, err := tx.Exec(stmt, "namespace_id", state.NamespaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
