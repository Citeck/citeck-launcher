package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// OpenOfflineStore opens an EXISTING profile without schema migration, snapshot
// journal replay, WAL deletion or directory creation. Caller must exclude the
// daemon for the entire operation. This is for surgical offline recovery only.
func OpenOfflineStore(home string, desktop bool) (Store, error) {
	root, err := fsutil.OpenCanonicalRoot(home)
	if err != nil {
		return nil, fmt.Errorf("open offline home: %w", err)
	}
	defer root.Close()
	key := "conf/namespace.yml"
	if desktop {
		key = "launcher.db"
	}
	parent, leaf, err := fsutil.RootedParent(root, key, false)
	if err != nil {
		return nil, fmt.Errorf("pin offline profile parent: %w", err)
	}
	defer parent.Close()
	f, err := fsutil.OpenRegularRooted(parent, leaf)
	if err != nil {
		return nil, fmt.Errorf("open offline profile: %w", err)
	}
	_ = f.Close()
	if !desktop {
		// An unfinished metadata transaction is not this command's to repair.
		if _, err = parent.Lstat("snapshot-state-transaction.json"); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("snapshot metadata recovery required before offline file reset")
		}
		base := filepath.Join(home, "conf")
		pinned, pinErr := root.OpenRoot(".")
		if pinErr != nil {
			return nil, fmt.Errorf("pin offline profile: %w", pinErr)
		}
		return &offlineFileStore{FileStore: &FileStore{baseDir: base, runtimeBase: filepath.Join(home, "data", "runtime"), mu: sharedFileStoreLock(base)}, root: pinned}, nil
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, e := parent.Lstat(leaf + suffix)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, fmt.Errorf("inspect offline database sidecar: %w", e)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("offline database sidecar must be regular")
		}
	}
	path, err := filepath.Abs(filepath.Join(home, "launcher.db"))
	if err != nil {
		return nil, fmt.Errorf("offline database path: %w", err)
	}
	u, err := offlineSQLiteURL(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open offline database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read offline database: %w", err)
	}
	u.RawQuery = "mode=rw&_pragma=busy_timeout(5000)"
	return &offlineSQLiteStore{SQLiteStore: &SQLiteStore{db: db}, writeDSN: u.String()}, nil
}

// SQLite file URIs require /C:/... for a local Windows drive: otherwise the
// drive is interpreted as an authority. UNC is deliberately refused rather
// than silently opening a different local path. The helper is testable on POSIX.
func offlineSQLiteURL(path string) (url.URL, error) {
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "//") {
		return url.URL{}, errors.New("offline recovery requires a local SQLite path, not UNC")
	}
	if len(path) >= 3 && path[1] == ':' && path[2] == '/' {
		path = "/" + path
	}
	if !strings.HasPrefix(path, "/") {
		return url.URL{}, errors.New("offline SQLite path must be absolute")
	}
	return url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}, nil
}

// Preview/validation never opens SQLite writable or runs the normal store's
// TRUNCATE checkpoint. The write connection is created only when the caller
// actually saves, after its private backup has reached disk.
type offlineSQLiteStore struct {
	*SQLiteStore
	writeDSN string
}

func (s *offlineSQLiteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close offline database: %w", err)
	}
	return nil
}
func (s *offlineSQLiteStore) SaveNamespaceState(ws, ns, status, raw string) error {
	db, err := sql.Open("sqlite", s.writeDSN)
	if err != nil {
		return fmt.Errorf("open offline state writer: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	writer := &SQLiteStore{db: db}
	return writer.SaveNamespaceState(ws, ns, status, raw)
}

// Namespace recovery uses pinned no-follow reads/writes even for server state:
// a runtime directory may have been made unsafe by the very fault being repaired.
type offlineFileStore struct {
	*FileStore
	root *os.Root
}

func (s *offlineFileStore) Close() error {
	if err := s.root.Close(); err != nil {
		return fmt.Errorf("close offline profile root: %w", err)
	}
	return nil
}
func (s *offlineFileStore) read(key string) (content string, found bool, err error) {
	parent, leaf, err := fsutil.RootedParent(s.root, key, false)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open offline state entry: %w", err)
	}
	defer parent.Close()
	f, err := fsutil.OpenRegularRooted(parent, leaf)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open offline state entry: %w", err)
	}
	defer f.Close()
	const maxState = 64 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxState+1))
	if err != nil {
		return "", false, fmt.Errorf("read offline state: %w", err)
	}
	if len(data) > maxState {
		return "", false, errors.New("offline state exceeds 64 MiB")
	}
	return string(data), true, nil
}
func (s *offlineFileStore) LoadNamespaceConfig(_, _ string) (configYAML string, found bool, err error) {
	return s.read("conf/namespace.yml")
}
func (s *offlineFileStore) LoadNamespaceState(_, ns string) (stateJSON string, found bool, err error) {
	return s.read("data/runtime/" + ns + "/state-" + ns + ".json")
}
func (s *offlineFileStore) SaveNamespaceState(_, ns, _, raw string) error {
	parent, leaf, err := fsutil.RootedParent(s.root, "data/runtime/"+ns+"/state-"+ns+".json", false)
	if err != nil {
		return fmt.Errorf("pin offline state writer: %w", err)
	}
	defer parent.Close()
	if err = fsutil.AtomicWriteRooted(parent, leaf, []byte(raw), 0o600); err != nil {
		return fmt.Errorf("write offline state: %w", err)
	}
	return nil
}
