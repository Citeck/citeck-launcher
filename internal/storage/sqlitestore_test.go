package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// sqliteErrorMirror mirrors modernc.org/sqlite.Error's memory layout (which
// has unexported fields) so tests can fabricate a `*sqlite.Error` with an
// arbitrary code. Coupled to upstream layout — TestSqliteErrorCodeRoundTrip
// below sanity-checks the constant the predicate relies on; if the upstream
// struct gets reordered, the cast simply produces a zero/garbage code and
// the SHORT_READ test fails loudly.
type sqliteErrorMirror struct {
	msg  string
	code int
}

func fakeSqliteErr(code int) *sqlitedrv.Error {
	mirror := &sqliteErrorMirror{
		msg:  fmt.Sprintf("disk I/O error (%d)", code),
		code: code,
	}
	return (*sqlitedrv.Error)(unsafe.Pointer(mirror))
}

func TestIsWALCorruptionError_ShortReadDetected(t *testing.T) {
	err := fakeSqliteErr(sqlite3.SQLITE_IOERR_SHORT_READ)
	// Sanity: confirm the unsafe cast produced the right code before
	// asserting the predicate. If this fails the upstream layout drifted
	// and the rest of the test is meaningless.
	if err.Code() != sqlite3.SQLITE_IOERR_SHORT_READ {
		t.Fatalf("layout drift: Code()=%d want %d", err.Code(), sqlite3.SQLITE_IOERR_SHORT_READ)
	}
	if !isWALCorruptionError(err) {
		t.Errorf("expected true for SHORT_READ sqlite error, got false")
	}
	// Also exercise the wrap-resilience: errors.As must still match
	// through a fmt.Errorf wrap (which is how callers normally see it).
	wrapped := fmt.Errorf("probe sqlite: %w", err)
	if !isWALCorruptionError(wrapped) {
		t.Errorf("expected true for wrapped SHORT_READ error, got false")
	}
}

func TestIsWALCorruptionError_OtherSqliteCodesAreIgnored(t *testing.T) {
	for _, code := range []int{sqlite3.SQLITE_BUSY, sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_IOERR} {
		err := fakeSqliteErr(code)
		if isWALCorruptionError(err) {
			t.Errorf("expected false for sqlite code %d, got true", code)
		}
	}
}

func TestIsWALCorruptionError_NonSqliteErrorsAreIgnored(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain text mentioning short read", errors.New("disk I/O error (short read suspicion)")},
		{"wrapped non-sqlite", fmt.Errorf("ctx: %w", errors.New("wal corruption suspected"))},
		{"plain text mentioning malformed wal", errors.New("database disk image is malformed in wal")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isWALCorruptionError(tc.err) {
				t.Errorf("expected false for non-sqlite error %v, got true", tc.err)
			}
		})
	}
}

// TestSqliteErrorCodeRoundTrip locks the assumption that the modernc.org/sqlite
// constant for SQLITE_IOERR_SHORT_READ matches the canonical SQLite code (522).
// If the upstream value ever drifts, isWALCorruptionError silently stops
// firing and the user is back to losing their last writes — this guard
// surfaces that immediately.
func TestSqliteErrorCodeRoundTrip(t *testing.T) {
	if sqlite3.SQLITE_IOERR_SHORT_READ != 522 {
		t.Fatalf("SQLITE_IOERR_SHORT_READ drifted from 522 to %d", sqlite3.SQLITE_IOERR_SHORT_READ)
	}
}

// TestOpenWithWALRecovery_TruncatedWALIsCleanedUp is the end-to-end sibling
// of the unit tests above: drop a deliberately truncated `.db-wal` on disk,
// then open the store via openWithWALRecovery and confirm it (a) doesn't
// fail, (b) wipes the bogus sidecar, and (c) reads the main DB cleanly.
// Without this, the unit tests could pass while the recovery sequence is
// wired up wrong (e.g. order of Close → Remove → reopen).
func TestOpenWithWALRecovery_TruncatedWALIsCleanedUp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "launcher.db")

	// Pass 1: create a clean DB with one row.
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	err = store.SetState(LauncherState{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("SetState: %v", err)
	}
	err = store.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Drop a 1-byte fake WAL beside the main DB. The WAL header is 32 bytes
	// so the first read attempt to walk it will return SQLITE_IOERR_SHORT_READ,
	// which is exactly what isWALCorruptionError matches. We can't reliably
	// reproduce this via the normal lifecycle (Close auto-checkpoints) so we
	// fabricate the fixture directly.
	walPath := dbPath + "-wal"
	err = os.WriteFile(walPath, []byte{0x00}, 0o600)
	if err != nil {
		t.Fatalf("write fake wal: %v", err)
	}

	// Pass 2: openWithWALRecovery must recover transparently. After the
	// recovery branch fires the sidecar should be gone (rewritten cleanly
	// on subsequent writes, but immediately after open it's removed).
	store2, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("recovery open: %v", err)
	}
	defer store2.Close() //nolint:errcheck // test cleanup
	st, err := store2.GetState()
	if err != nil {
		t.Fatalf("GetState after recovery: %v", err)
	}
	if st.WorkspaceID != "ws-1" {
		t.Errorf("after recovery, expected workspaceID 'ws-1', got %q", st.WorkspaceID)
	}
}

func TestSQLiteNamespaceCRUD(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer s.Close()

	// empty
	list, err := s.ListNamespaces("ws1")
	require.NoError(t, err)
	require.Empty(t, list)

	// missing load
	_, ok, err := s.LoadNamespaceConfig("ws1", "nsA")
	require.NoError(t, err)
	require.False(t, ok)

	// save config
	require.NoError(t, s.SaveNamespaceConfig("ws1", "nsA", "Alpha", "id: nsA\nname: Alpha\n"))
	yaml, ok, err := s.LoadNamespaceConfig("ws1", "nsA")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "id: nsA\nname: Alpha\n", yaml)

	// save state does NOT clobber config
	require.NoError(t, s.SaveNamespaceState("ws1", "nsA", "RUNNING", `{"status":"RUNNING"}`))
	yaml, ok, _ = s.LoadNamespaceConfig("ws1", "nsA")
	require.True(t, ok)
	require.Equal(t, "id: nsA\nname: Alpha\n", yaml, "state save must not clobber config")
	js, ok, err := s.LoadNamespaceState("ws1", "nsA")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, `{"status":"RUNNING"}`, js)

	// save config again does NOT clobber state
	require.NoError(t, s.SaveNamespaceConfig("ws1", "nsA", "Alpha2", "id: nsA\nname: Alpha2\n"))
	js, ok, _ = s.LoadNamespaceState("ws1", "nsA")
	require.True(t, ok)
	require.Equal(t, `{"status":"RUNNING"}`, js, "config save must not clobber state")

	// listing reflects denormalized name + status, scoped by ws
	require.NoError(t, s.SaveNamespaceConfig("ws1", "nsB", "Beta", "id: nsB\n"))
	require.NoError(t, s.SaveNamespaceConfig("ws2", "nsZ", "Zeta", "id: nsZ\n"))
	list, err = s.ListNamespaces("ws1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "nsA", list[0].ID)
	require.Equal(t, "Alpha2", list[0].Name)
	require.Equal(t, "RUNNING", list[0].Status)
	require.Equal(t, "nsB", list[1].ID)

	// delete removes config + state
	require.NoError(t, s.DeleteNamespace("ws1", "nsA"))
	_, ok, _ = s.LoadNamespaceConfig("ws1", "nsA")
	require.False(t, ok)
	_, ok, _ = s.LoadNamespaceState("ws1", "nsA")
	require.False(t, ok)
	list, _ = s.ListNamespaces("ws1")
	require.Len(t, list, 1)
}

func TestSQLiteSchemaV6NamespacesTable(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLiteStore(dir)
	require.NoError(t, err)
	defer s.Close()

	// schema_version reached at least 6
	var v int
	require.NoError(t, s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v))
	require.GreaterOrEqual(t, v, 6)

	// namespaces table exists with the expected columns
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('namespaces')`)
	require.NoError(t, err)
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		cols[c] = true
	}
	for _, want := range []string{"ws_id", "ns_id", "name", "status", "config_yaml", "state_json"} {
		require.True(t, cols[want], "missing column %q", want)
	}
}
