package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	sqlitedrv "modernc.org/sqlite" // Pure Go SQLite driver — named for *sqlitedrv.Error
	sqlite3 "modernc.org/sqlite/lib"
)

// openWithWALRecovery opens launcher.db. If the open succeeds but the first
// real query reveals an unclean WAL state (SQLITE_IOERR_SHORT_READ — the
// code modernc.org/sqlite emits when the WAL header or last frame is
// truncated), it closes the DB, removes the WAL+SHM sidecars, and reopens.
// The main DB file itself is never touched here — only the recoverable
// journal. The previous implementation removed the sidecars unconditionally
// on every start, which silently dropped the last session's committed-but-
// not-checkpointed writes.
func openWithWALRecovery(dbPath string) (*sql.DB, error) {
	connStr := dbPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Touch the DB to surface WAL-replay errors. sql.Open is lazy; quick_check
	// is the cheapest call that forces the recovery path to run.
	if _, probeErr := db.Exec("PRAGMA quick_check"); probeErr != nil {
		if !isWALCorruptionError(probeErr) {
			_ = db.Close()
			return nil, fmt.Errorf("probe sqlite: %w", probeErr)
		}
		slog.Warn("SQLite WAL appears corrupt, removing journal sidecars and retrying",
			"err", probeErr, "wal", dbPath+"-wal", "shm", dbPath+"-shm")
		_ = db.Close()
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
		db, err = sql.Open("sqlite", connStr)
		if err != nil {
			return nil, fmt.Errorf("reopen sqlite after wal cleanup: %w", err)
		}
		if _, retryErr := db.Exec("PRAGMA quick_check"); retryErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite still failing after wal cleanup: %w", retryErr)
		}
	}
	return db, nil
}

// isWALCorruptionError matches the error code emitted when the WAL/SHM
// sidecars survive an unclean shutdown without matching the main DB.
// Match by `sqlite.Error.Code()` because modernc.org/sqlite renders
// IOERR_SHORT_READ as `"disk I/O error (522)"` — substring matching on
// `"short read"` never fires. We deliberately do NOT match generic IO
// errors (permission denied, ENOENT on the main DB) — those need to bubble
// up so the caller sees the real problem instead of a silent data wipe.
func isWALCorruptionError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlitedrv.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlite3.SQLITE_IOERR_SHORT_READ
}

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
	// WAL/SHM files are NOT swept on open. In WAL mode they hold
	// committed transactions that haven't been checkpointed into the
	// main DB yet — wiping them on every start silently drops the
	// previous session's last writes (that's how 'Exit to Welcome
	// doesn't persist' hid). They're only deleted as a recovery action
	// when the open itself fails with a WAL-corruption signature.
	db, err := openWithWALRecovery(dbPath)
	if err != nil {
		return nil, err
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
		{
			// v2 — Kotlin-parity workspace fields: repo pull period (ISO 8601,
			// default "PT2H" = 2h) and auth type (NONE / TOKEN).
			version: 2,
			stmts: []string{
				`ALTER TABLE workspaces ADD COLUMN repo_pull_period TEXT NOT NULL DEFAULT 'PT2H'`,
				`ALTER TABLE workspaces ADD COLUMN auth_type TEXT NOT NULL DEFAULT 'NONE'`,
			},
		},
		{
			// v3 — typed BASIC_AUTH: store username separately so passwords
			// containing ':' round-trip untouched. Legacy "user:pass" rows
			// are split in-place once. The split is intentionally limited to
			// BASIC_AUTH / REGISTRY_AUTH; GIT_TOKEN values often contain ':'
			// in PATs themselves (no user component) and must NOT be split.
			version: 3,
			stmts: []string{
				`ALTER TABLE secrets ADD COLUMN username TEXT NOT NULL DEFAULT ''`,
				`UPDATE secrets
				 SET username = substr(value, 1, instr(value, ':') - 1),
				     value    = substr(value, instr(value, ':') + 1)
				 WHERE type IN ('BASIC_AUTH', 'REGISTRY_AUTH')
				   AND username = ''
				   AND instr(value, ':') > 0`,
			},
		},
		{
			// v4 — per-workspace namespace selection (Kotlin parity:
			// workspace-state/{wsId} → SELECTED_NS_PROP). The legacy global
			// namespace_id key is folded into selected_ns under the current
			// workspace_id, then dropped.
			version: 4,
			stmts: []string{
				`INSERT INTO launcher_state (key, value)
				 SELECT 'selected_ns',
				        json_object(
				            COALESCE((SELECT value FROM launcher_state WHERE key = 'workspace_id'), ''),
				            (SELECT value FROM launcher_state WHERE key = 'namespace_id')
				        )
				 WHERE EXISTS (SELECT 1 FROM launcher_state WHERE key = 'namespace_id' AND value != '')
				   AND EXISTS (SELECT 1 FROM launcher_state WHERE key = 'workspace_id' AND value != '')
				 ON CONFLICT(key) DO NOTHING`,
				`DELETE FROM launcher_state WHERE key = 'namespace_id'`,
			},
		},
		{
			// v5 — per-repo git sync metadata (Kotlin parity: git-repo!instances
			// in GitRepoService). Survives restart so an idle launcher does not
			// re-pull every workspace/bundle repo on cold start.
			version: 5,
			stmts: []string{
				`CREATE TABLE IF NOT EXISTS git_repo_state (
					path TEXT PRIMARY KEY,
					last_sync_ms INTEGER NOT NULL DEFAULT 0,
					last_commit_hash TEXT NOT NULL DEFAULT ''
				)`,
			},
		},
		{
			// v6 — namespace config + per-NS runtime state moved off disk into
			// the DB (desktop). config_yaml is the exact namespace.yml text;
			// state_json is the exact state-{nsID}.json. name/status are
			// denormalized so the list query never parses a blob. Enumeration
			// by row (not directory scan) removes the ghost-namespace class.
			version: 6,
			stmts: []string{
				`CREATE TABLE IF NOT EXISTS namespaces (
					ws_id       TEXT NOT NULL,
					ns_id       TEXT NOT NULL,
					name        TEXT NOT NULL DEFAULT '',
					status      TEXT NOT NULL DEFAULT '',
					config_yaml TEXT NOT NULL DEFAULT '',
					state_json  TEXT NOT NULL DEFAULT '',
					PRIMARY KEY (ws_id, ns_id)
				)`,
			},
		},
		{
			// v7 — reusable workspace repo-auth secrets: secret_id references a
			// shared secret row (one GitLab token for N workspaces). Empty keeps
			// the legacy per-workspace "ws:{id}:repo" secret-key convention.
			version: 7,
			stmts: []string{
				`ALTER TABLE workspaces ADD COLUMN secret_id TEXT NOT NULL DEFAULT ''`,
			},
		},
		{
			// v8 — host-tagged secrets for the host-filtered registry/git secret
			// picker. Backfill legacy REGISTRY_AUTH secrets from their
			// "images-repo:<host>" scope so they show up (and stay editable) in
			// the standard picker filtered by that host.
			version: 8,
			stmts: []string{
				`ALTER TABLE secrets ADD COLUMN host TEXT NOT NULL DEFAULT ''`,
				`UPDATE secrets
				 SET host = substr(scope, length('images-repo:') + 1)
				 WHERE host = ''
				   AND type = 'REGISTRY_AUTH'
				   AND scope LIKE 'images-repo:%'`,
			},
		},
		{
			// v9 — per-workspace registry auth bindings: host → secret id, so a
			// stored REGISTRY_AUTH credential is reused across namespaces/
			// workspaces instead of being re-entered for every registry host.
			version: 9,
			stmts: []string{
				`CREATE TABLE IF NOT EXISTS registry_bindings (
					ws_id     TEXT NOT NULL,
					host      TEXT NOT NULL,
					secret_id TEXT NOT NULL,
					PRIMARY KEY (ws_id, host)
				)`,
			},
		},
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
	rows, err := s.db.Query(`SELECT id, name, repo_url, repo_branch, repo_pull_period, auth_type, secret_id
		FROM workspaces ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query workspaces: %w", err)
	}
	defer rows.Close()

	var result []WorkspaceDto
	for rows.Next() {
		var ws WorkspaceDto
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch, &ws.RepoPullPeriod, &ws.AuthType, &ws.SecretID); err != nil {
			return nil, fmt.Errorf("scan workspace row: %w", err)
		}
		result = append(result, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return result, nil
}

// GetWorkspace returns a single workspace by ID, or (nil, nil) when the
// workspace does not exist. Callers should branch on the nil return rather
// than treating "not found" as an error — this matches FileStore semantics
// and lets the daemon handlers map missing-workspace to 404 cleanly.
func (s *SQLiteStore) GetWorkspace(id string) (*WorkspaceDto, error) {
	var ws WorkspaceDto
	err := s.db.QueryRow(
		`SELECT id, name, repo_url, repo_branch, repo_pull_period, auth_type, secret_id
		 FROM workspaces WHERE id = ?`, id,
	).Scan(&ws.ID, &ws.Name, &ws.RepoURL, &ws.RepoBranch, &ws.RepoPullPeriod, &ws.AuthType, &ws.SecretID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query workspace %s: %w", id, err)
	}
	return &ws, nil
}

// SaveWorkspace inserts or updates a workspace (upsert).
// Defaults are applied for empty RepoPullPeriod ("PT2H") and AuthType ("NONE")
// so callers in older code paths (e.g. h2migrate, legacy desktop fallback) get
// the Kotlin-parity defaults without having to know the field exists.
func (s *SQLiteStore) SaveWorkspace(ws WorkspaceDto) error {
	if ws.RepoPullPeriod == "" {
		ws.RepoPullPeriod = "PT2H"
	}
	if ws.AuthType == "" {
		ws.AuthType = "NONE"
	}
	_, err := s.db.Exec(`
		INSERT INTO workspaces (id, name, repo_url, repo_branch, repo_pull_period, auth_type, secret_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			repo_url=excluded.repo_url,
			repo_branch=excluded.repo_branch,
			repo_pull_period=excluded.repo_pull_period,
			auth_type=excluded.auth_type,
			secret_id=excluded.secret_id
	`, ws.ID, ws.Name, ws.RepoURL, ws.RepoBranch, ws.RepoPullPeriod, ws.AuthType, ws.SecretID)
	if err != nil {
		return fmt.Errorf("save workspace %s: %w", ws.ID, err)
	}
	return nil
}

// DeleteWorkspace removes a workspace by ID, along with its workspace-owned
// registry bindings (the junction table has no FK cascade). Both deletes run in
// one transaction so a failure on the second can't orphan the bindings under a
// dead ws_id.
func (s *SQLiteStore) DeleteWorkspace(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete workspace %s: begin: %w", id, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM workspaces WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete workspace %s: %w", id, err)
	}
	// Cascade the workspace's namespaces (config + state rows) — otherwise they
	// orphan in the DB and, worse, keep SweepOrphans protecting their Docker
	// volumes (the keep-set is built from stored namespaces), so the data would
	// never be reclaimed.
	if _, err := tx.Exec("DELETE FROM namespaces WHERE ws_id = ?", id); err != nil {
		return fmt.Errorf("delete workspace %s namespaces: %w", id, err)
	}
	if _, err := tx.Exec("DELETE FROM registry_bindings WHERE ws_id = ?", id); err != nil {
		return fmt.Errorf("delete workspace %s registry bindings: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete workspace %s: commit: %w", id, err)
	}
	return nil
}

// --- Secrets ---

// ListSecrets returns metadata for all secrets (without values). Username is
// included — it is stored plaintext (only Value is encrypted) and the
// write-only edit form prefills it from this listing.
func (s *SQLiteStore) ListSecrets() ([]SecretMeta, error) {
	rows, err := s.db.Query("SELECT id, name, type, scope, host, username, created_at FROM secrets ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query secrets: %w", err)
	}
	defer rows.Close()

	var result []SecretMeta
	for rows.Next() {
		var sm SecretMeta
		var createdStr string
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.Type, &sm.Scope, &sm.Host, &sm.Username, &createdStr); err != nil {
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
		"SELECT id, name, type, value, username, scope, host, created_at FROM secrets WHERE id = ?", id,
	).Scan(&sec.ID, &sec.Name, &sec.Type, &sec.Value, &sec.Username, &sec.Scope, &sec.Host, &createdStr)
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
		INSERT INTO secrets (id, name, type, value, username, scope, host, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, value=excluded.value, username=excluded.username, scope=excluded.scope, host=excluded.host
	`, secret.ID, secret.Name, secret.Type, secret.Value, secret.Username, secret.Scope, secret.Host, secret.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save secret %s: %w", secret.ID, err)
	}
	return nil
}

// DeleteSecret removes a secret by ID, cascading to any registry bindings that
// referenced it (across ALL workspaces — secrets are global, bindings are
// per-workspace, and the junction table has no FK cascade). Without this a
// deleted registry credential leaves a dangling host→secret binding that the
// credentials dialog renders as "(not found)". Both deletes run in one
// transaction so a failure on the second can't orphan a binding under a gone
// secret.
func (s *SQLiteStore) DeleteSecret(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete secret %s: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	if _, err := tx.Exec("DELETE FROM secrets WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete secret %s: %w", id, err)
	}
	if _, err := tx.Exec("DELETE FROM registry_bindings WHERE secret_id = ?", id); err != nil {
		return fmt.Errorf("delete secret %s registry bindings: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete secret %s: commit: %w", id, err)
	}
	return nil
}

// ListRegistryBindings returns the workspace's host → secret-id registry auth
// bindings.
func (s *SQLiteStore) ListRegistryBindings(wsID string) (map[string]string, error) {
	rows, err := s.db.Query("SELECT host, secret_id FROM registry_bindings WHERE ws_id = ?", wsID)
	if err != nil {
		return nil, fmt.Errorf("query registry bindings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var host, secretID string
		if err := rows.Scan(&host, &secretID); err != nil {
			return nil, fmt.Errorf("scan registry binding: %w", err)
		}
		out[host] = secretID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registry bindings: %w", err)
	}
	return out, nil
}

// SetRegistryBinding upserts a host → secret-id binding for the workspace; an
// empty secretID removes the binding.
func (s *SQLiteStore) SetRegistryBinding(wsID, host, secretID string) error {
	if secretID == "" {
		if _, err := s.db.Exec("DELETE FROM registry_bindings WHERE ws_id = ? AND host = ?", wsID, host); err != nil {
			return fmt.Errorf("delete registry binding: %w", err)
		}
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO registry_bindings (ws_id, host, secret_id) VALUES (?, ?, ?)
		ON CONFLICT(ws_id, host) DO UPDATE SET secret_id = excluded.secret_id
	`, wsID, host, secretID)
	if err != nil {
		return fmt.Errorf("set registry binding: %w", err)
	}
	return nil
}

// --- Launcher State ---

// GetState returns the persisted launcher state.
func (s *SQLiteStore) GetState() (*LauncherState, error) {
	state := &LauncherState{}
	_ = s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'workspace_id'").Scan(&state.WorkspaceID)
	var selectedNsJSON string
	if err := s.db.QueryRow("SELECT value FROM launcher_state WHERE key = 'selected_ns'").Scan(&selectedNsJSON); err == nil && selectedNsJSON != "" {
		_ = json.Unmarshal([]byte(selectedNsJSON), &state.SelectedNs)
	}
	return state, nil
}

// SetState persists the launcher state (workspace and per-workspace namespace selection).
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
	selectedNsJSON := ""
	if len(state.SelectedNs) > 0 {
		buf, err := json.Marshal(state.SelectedNs)
		if err != nil {
			return fmt.Errorf("marshal selected_ns: %w", err)
		}
		selectedNsJSON = string(buf)
	}
	if _, err := tx.Exec(stmt, "selected_ns", selectedNsJSON); err != nil {
		return fmt.Errorf("set selected_ns: %w", err)
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

// --- Git Repo State ---

// GetGitRepoState returns the persisted sync metadata for a repo path, or
// (nil, nil) when no row exists. Matches the FileStore convention: callers
// branch on the nil pointer rather than treating "not found" as an error.
func (s *SQLiteStore) GetGitRepoState(path string) (*GitRepoState, error) {
	var state GitRepoState
	err := s.db.QueryRow(
		`SELECT path, last_sync_ms, last_commit_hash FROM git_repo_state WHERE path = ?`, path,
	).Scan(&state.Path, &state.LastSyncMs, &state.LastCommitHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query git repo state %s: %w", path, err)
	}
	return &state, nil
}

// SetGitRepoState upserts a git_repo_state row.
func (s *SQLiteStore) SetGitRepoState(state GitRepoState) error {
	_, err := s.db.Exec(`
		INSERT INTO git_repo_state (path, last_sync_ms, last_commit_hash) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			last_sync_ms=excluded.last_sync_ms,
			last_commit_hash=excluded.last_commit_hash
	`, state.Path, state.LastSyncMs, state.LastCommitHash)
	if err != nil {
		return fmt.Errorf("save git repo state %s: %w", state.Path, err)
	}
	return nil
}

// ListGitRepoStates returns every git_repo_state row ordered by path.
func (s *SQLiteStore) ListGitRepoStates() ([]GitRepoState, error) {
	rows, err := s.db.Query(`SELECT path, last_sync_ms, last_commit_hash FROM git_repo_state ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("query git repo states: %w", err)
	}
	defer rows.Close()

	var out []GitRepoState
	for rows.Next() {
		var st GitRepoState
		if err := rows.Scan(&st.Path, &st.LastSyncMs, &st.LastCommitHash); err != nil {
			return nil, fmt.Errorf("scan git repo state row: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate git repo states: %w", err)
	}
	return out, nil
}

// --- Namespaces (config + per-NS runtime state, keyed-blob) ---

// ListNamespaces returns id/name/status rows for a workspace, ordered by ns_id.
func (s *SQLiteStore) ListNamespaces(wsID string) ([]NamespaceRow, error) {
	rows, err := s.db.Query(
		`SELECT ns_id, name, status FROM namespaces WHERE ws_id = ? ORDER BY ns_id`, wsID)
	if err != nil {
		return nil, fmt.Errorf("query namespaces for %s: %w", wsID, err)
	}
	defer rows.Close()
	var out []NamespaceRow
	for rows.Next() {
		var r NamespaceRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Status); err != nil {
			return nil, fmt.Errorf("scan namespace row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate namespaces: %w", err)
	}
	return out, nil
}

// LoadNamespaceConfig returns the stored config YAML for a namespace (ok=false if absent).
func (s *SQLiteStore) LoadNamespaceConfig(wsID, nsID string) (configYAML string, ok bool, err error) {
	var y string
	err = s.db.QueryRow(
		`SELECT config_yaml FROM namespaces WHERE ws_id = ? AND ns_id = ?`, wsID, nsID).Scan(&y)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query namespace config %s/%s: %w", wsID, nsID, err)
	}
	if y == "" {
		return "", false, nil
	}
	return y, true, nil
}

// SaveNamespaceConfig upserts the config YAML + denormalized name (state row untouched).
func (s *SQLiteStore) SaveNamespaceConfig(wsID, nsID, name, configYAML string) error {
	_, err := s.db.Exec(`
		INSERT INTO namespaces (ws_id, ns_id, name, config_yaml) VALUES (?, ?, ?, ?)
		ON CONFLICT(ws_id, ns_id) DO UPDATE SET name=excluded.name, config_yaml=excluded.config_yaml
	`, wsID, nsID, name, configYAML)
	if err != nil {
		return fmt.Errorf("save namespace config %s/%s: %w", wsID, nsID, err)
	}
	return nil
}

// LoadNamespaceState returns the stored runtime-state JSON for a namespace (ok=false if absent).
func (s *SQLiteStore) LoadNamespaceState(wsID, nsID string) (stateJSON string, ok bool, err error) {
	var j string
	err = s.db.QueryRow(
		`SELECT state_json FROM namespaces WHERE ws_id = ? AND ns_id = ?`, wsID, nsID).Scan(&j)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query namespace state %s/%s: %w", wsID, nsID, err)
	}
	if j == "" {
		return "", false, nil
	}
	return j, true, nil
}

// SaveNamespaceState upserts the runtime-state JSON + denormalized status (config untouched).
func (s *SQLiteStore) SaveNamespaceState(wsID, nsID, status, stateJSON string) error {
	_, err := s.db.Exec(`
		INSERT INTO namespaces (ws_id, ns_id, status, state_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(ws_id, ns_id) DO UPDATE SET status=excluded.status, state_json=excluded.state_json
	`, wsID, nsID, status, stateJSON)
	if err != nil {
		return fmt.Errorf("save namespace state %s/%s: %w", wsID, nsID, err)
	}
	return nil
}

// DeleteNamespace removes the namespace's config + state row.
func (s *SQLiteStore) DeleteNamespace(wsID, nsID string) error {
	if _, err := s.db.Exec(`DELETE FROM namespaces WHERE ws_id = ? AND ns_id = ?`, wsID, nsID); err != nil {
		return fmt.Errorf("delete namespace %s/%s: %w", wsID, nsID, err)
	}
	return nil
}

// DB returns the underlying *sql.DB for transactional operations.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// Close releases the database connection. Runs a TRUNCATE checkpoint
// beforehand so the WAL is folded into the main DB and the next process
// sees an empty WAL — independent of whether the OS / sqlite recovery
// path keeps the WAL file around between sessions.
func (s *SQLiteStore) Close() error {
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		// Non-fatal: WAL recovery will still replay it on next open. Log via
		// the returned error so callers can decide; we still attempt Close.
		// (Most callers ignore Close errors, which is fine here.)
		closeErr := s.db.Close()
		if closeErr != nil {
			return fmt.Errorf("close database (checkpoint failed: %w): %w", err, closeErr)
		}
		return fmt.Errorf("checkpoint database: %w", err)
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}
