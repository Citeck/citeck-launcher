package launchermcp

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Shared driver, never the launcher's WAL-recovery opener.
)

//go:embed schema.sql
var schema string

//go:embed oauth_pending_schema.sql
var oauthPendingSchema string

func sqliteDSN(path string) string {
	native := filepath.ToSlash(path)
	// A drive path must be file:///C:/..., not file:C:\... . UNC paths
	// retain their double slash in the path (an empty URI authority).
	u := url.URL{Scheme: "file", Path: native}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(native, "//") {
			u.Path = "/" + native
		}
	}
	q := url.Values{}
	q.Set("_txlock", "immediate")
	pragmas := []string{"journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)", "temp_store(MEMORY)", "cache_size(-8192)", "busy_timeout(1000)"}
	if runtime.GOOS == "darwin" {
		pragmas = append(pragmas, "fullfsync(ON)", "checkpoint_fullfsync(ON)")
	}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func verifyPragmas(q queryer) error {
	expected := map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "temp_store": "2", "cache_size": "-8192", "busy_timeout": "1000"}
	if runtime.GOOS == "darwin" {
		expected["fullfsync"] = "1"
		expected["checkpoint_fullfsync"] = "1"
	}
	for k, v := range expected {
		var actual any
		if err := q.QueryRowContext(context.Background(), "PRAGMA "+k).Scan(&actual); err != nil {
			return mcpStorageError(err)
		}
		if fmt.Sprint(actual) != v {
			return fmt.Errorf("MCP SQLite policy %s not applied", k)
		}
	}
	return nil
}

// OpenStore opens mcp.db next to the legacy state.json (also accepts that old
// argument spelling). It never falls back to JSON after SQL accepted authority.
// SQLite uses a pathname: owner-private stable parents, not os.Root, protect it.
func OpenStore(path string) (store *Store, err error) {
	dir, err := prepareDBDirectory(filepath.Dir(path))
	if err != nil {
		return nil, mcpStorageError(err)
	}
	if err = checkDBFiles(dir); err != nil {
		return nil, mcpStorageError(err)
	}
	lock, err := lockDBOwner(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return nil, mcpStorageError(err)
	}
	s := &Store{dir: dir, path: filepath.Join(dir, "mcp.db"), lock: lock, revocationDebt: map[string]time.Time{}}
	defer func() {
		if err != nil {
			var cleanup error
			if s.db != nil {
				cleanup = s.db.Close()
			}
			err = errors.Join(err, cleanup, s.lock.Close())
		}
	}()
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, mcpStorageError(err)
	}
	_, e := os.Lstat(s.path)
	fresh := os.IsNotExist(e)
	if e != nil && !fresh {
		return nil, mcpStorageError(e)
	}
	if fresh {
		if createErr := s.createDBFile(); createErr != nil {
			return nil, createErr
		}
	}
	if err = checkDBFiles(dir); err != nil {
		return nil, mcpStorageError(err)
	}
	dbBefore, err := os.Lstat(s.path)
	if err != nil {
		return nil, mcpStorageError(err)
	}
	s.db, err = sql.Open("sqlite", sqliteDSN(s.path))
	if err != nil {
		return nil, mcpStorageError(err)
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	if err = s.db.Ping(); err != nil {
		return nil, mcpStorageError(err)
	}
	if err = verifyPragmas(s.db); err != nil {
		return nil, mcpStorageError(err)
	}
	if identityErr := checkDBIdentity(dir, s.path, before, dbBefore); identityErr != nil {
		return nil, identityErr
	}
	if err = checkDBFiles(dir); err != nil {
		return nil, mcpStorageError(err)
	}
	if schemaErr := s.prepareSchema(fresh); schemaErr != nil {
		return nil, schemaErr
	}
	if err = s.validateAuthority(); err != nil {
		return nil, mcpStorageError(err)
	}
	if err = s.markSQLAuthority(); err != nil {
		return nil, mcpStorageError(err)
	}
	if err = s.db.QueryRow("SELECT key FROM mcp_root WHERE id=1").Scan(&s.key); err != nil {
		return nil, mcpStorageError(err)
	}
	settings, err := s.readSettings(s.db)
	if err != nil {
		return nil, mcpStorageError(err)
	}
	s.settingsCache = settings.Settings
	if err = checkDBFiles(dir); err != nil {
		return nil, mcpStorageError(err)
	}
	if err = syncDBDirectory(dir); err != nil {
		return nil, mcpStorageError(err)
	}
	// Unfinished browser ceremonies never survive restart, even if approved.
	if cancelErr := s.oauthWrite(context.Background(), func(ctx context.Context) error {
		_, e := s.transaction(ctx).q.ExecContext(ctx, "UPDATE oauth_pending SET status='cancelled' WHERE status IN('pending','approved')")
		return mcpStorageError(e)
	}); cancelErr != nil {
		return nil, cancelErr
	}
	s.oauth = newOAuthProvider(s)
	return s, nil
}
func checkDBFiles(dir string) error {
	for _, name := range []string{"mcp.db", "mcp.db-wal", "mcp.db-shm", "mcp.db-journal", "owner.lock", "state.json", "legacy.backup"} {
		err := privatePermissions(filepath.Join(dir, name), false)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return mcpStorageError(err)
		}
	}
	return nil
}
func (s *Store) validateAuthority() error {
	var check string
	if err := s.db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return mcpStorageError(err)
	}
	if check != "ok" {
		return errors.New("MCP SQLite integrity check failed")
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return mcpStorageError(err)
	}
	bad := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return mcpStorageError(err)
	}
	if bad {
		return errors.New("MCP foreign key check failed")
	}
	for _, q := range []string{
		"SELECT count(*)!=1 FROM mcp_root WHERE id=1 AND typeof(key)='blob' AND length(key)=32",
		"SELECT count(*)!=1 FROM mcp_settings WHERE id=1 AND epoch>0 AND port BETWEEN 1024 AND 65535",
		"SELECT count(*) FROM oauth_grants WHERE approved<=0 OR deadline<=approved OR epoch<1 OR resource NOT LIKE 'http://127.0.0.1:%/mcp'",
		"SELECT count(*) FROM oauth_grants g JOIN oauth_clients c ON g.client=c.id WHERE g.revoked=0 AND g.epoch!=c.epoch",
		"SELECT count(*) FROM manual_tokens WHERE created<=0 OR expiry=0 OR last_used=0 OR revoked_at=0",
		"SELECT count(*) FROM oauth_access a JOIN oauth_grants g ON a.grant_id=g.id WHERE a.expiry>g.deadline",
		"SELECT count(*) FROM oauth_refresh_current a JOIN oauth_grants g ON a.grant_id=g.id WHERE a.expiry>g.deadline",
		"SELECT count(*) FROM oauth_refresh_used a JOIN oauth_grants g ON a.grant_id=g.id WHERE a.expiry>g.deadline",
		"SELECT count(*) FROM oauth_codes a JOIN oauth_grants g ON a.grant_id=g.id WHERE a.expiry>g.deadline",
		"SELECT count(*) FROM oauth_pkce a JOIN oauth_codes c ON a.signature=c.signature WHERE a.grant_id!=c.grant_id OR a.expiry!=c.expiry OR length(a.challenge)!=43",
	} {
		var n int
		if err = s.db.QueryRow(q).Scan(&n); err != nil {
			return mcpStorageError(err)
		}
		if n != 0 {
			return errors.New("invalid MCP database authority")
		}
	}
	// Client metadata is small and fully validated on startup; histories stay rows.
	rows, err = s.db.Query("SELECT id,name,redirects,created,expiry FROM oauth_clients")
	if err != nil {
		return mcpStorageError(err)
	}
	for rows.Next() {
		var c oauthClient
		var redirects string
		var created, expiry int64
		if err = rows.Scan(&c.ID, &c.Name, &redirects, &created, &expiry); err != nil {
			break
		}
		c.CreatedAt = time.Unix(0, created)
		c.ExpiresAt = time.Unix(0, expiry)
		if err = json.Unmarshal([]byte(redirects), &c.Redirects); err != nil {
			break
		}
		if err = validateOAuthClient(c.ID, c); err != nil {
			break
		}
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func (s *Store) createDBFile() error {
	raw, e := readOptional(filepath.Join(s.dir, "state.json"))
	if e != nil {
		return mcpStorageError(e)
	}
	var f map[string]json.RawMessage
	if len(raw) > 0 {
		if e = json.Unmarshal(raw, &f); e != nil {
			return mcpStorageError(e)
		}
	}
	if f["mcpSqlite"] != nil || f["mcpSqliteFence"] != nil {
		return errors.New("MCP SQL is missing after migration; refusing to recreate authority")
	}
	if _, e = parseLegacy(raw); e != nil {
		return mcpStorageError(e)
	}
	file, e := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if e != nil {
		return mcpStorageError(e)
	}
	if e = errors.Join(file.Sync(), file.Close()); e != nil {
		return mcpStorageError(e)
	}
	return nil
}

func checkDBIdentity(dir, path string, parent, file os.FileInfo) error {
	after, err := os.Lstat(dir)
	if err != nil || !os.SameFile(parent, after) {
		return errors.New("MCP private parent changed while opening")
	}
	after, err = os.Lstat(path)
	if err != nil || !os.SameFile(file, after) {
		return errors.New("MCP database changed while opening")
	}
	return nil
}

func (s *Store) prepareSchema(fresh bool) error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return mcpStorageError(err)
	}
	if version == 0 && fresh {
		raw, e := readOptional(filepath.Join(s.dir, "state.json"))
		if e != nil {
			return mcpStorageError(e)
		}
		if err := s.initialize(raw); err != nil {
			return mcpStorageError(err)
		}
		return nil
	} else if version != 1 && version != 2 && version != 3 && version != 4 {
		return errors.New("unsupported or incomplete MCP database; refusing to reset its key")
	}
	if version < 3 {
		if err := s.upgradeSchema(version); err != nil {
			return err
		}
	}
	if version < 4 {
		return s.upgradeApprovalSchema()
	}
	return nil
}
