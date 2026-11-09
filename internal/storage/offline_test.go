package storage

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOfflineStoreRequiresExistingProfileAndPreservesState(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(map[bool]string{false: "server", true: "desktop"}[desktop], func(t *testing.T) {
			home := t.TempDir()
			_, err := OpenOfflineStore(home, desktop)
			require.Error(t, err)
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Empty(t, entries, "must not create profile files")
			var original Store
			if desktop {
				original, err = NewSQLiteStore(home)
			} else {
				original, err = NewFileStore(filepath.Join(home, "conf"), filepath.Join(home, "data", "runtime"))
			}
			require.NoError(t, err)
			require.NoError(t, original.SaveNamespaceConfig("ws", "default", "test", "id: default\n"))
			require.NoError(t, original.SaveNamespaceState("ws", "default", "RUNNING", `{"status":"RUNNING","keep":42}`))
			require.NoError(t, original.Close())
			offline, err := OpenOfflineStore(home, desktop)
			require.NoError(t, err)
			defer offline.Close()
			cfg, ok, err := offline.LoadNamespaceConfig("ws", "default")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "id: default\n", cfg)
			raw, ok, err := offline.LoadNamespaceState("ws", "default")
			require.NoError(t, err)
			require.True(t, ok)
			require.JSONEq(t, `{"status":"RUNNING","keep":42}`, raw)
			require.NoError(t, offline.SaveNamespaceState("ws", "default", "STOPPED", `{"status":"STOPPED","keep":42}`))
			raw, _, err = offline.LoadNamespaceState("ws", "default")
			require.NoError(t, err)
			require.JSONEq(t, `{"status":"STOPPED","keep":42}`, raw)
		})
	}
}

func TestOfflineStoreRefusesSnapshotJournalWithoutReplayingIt(t *testing.T) {
	home := t.TempDir()
	conf := filepath.Join(home, "conf")
	require.NoError(t, os.MkdirAll(conf, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(conf, "namespace.yml"), []byte("id: default\n"), 0o600))
	path := filepath.Join(conf, "snapshot-state-transaction.json")
	raw := []byte(`{"uncommitted":"must stay"}`)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	_, err := OpenOfflineStore(home, false)
	require.Error(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, after)
}

func TestOfflineSQLiteURIsKeepDriveAndEscapedPathOutOfAuthority(t *testing.T) {
	for _, path := range []string{"C:/Users/Roman A/profile?#/launcher.db", "/tmp/profile ?#/launcher.db"} {
		u, err := offlineSQLiteURL(path)
		require.NoError(t, err)
		parsed, err := url.Parse(u.String())
		require.NoError(t, err)
		require.Empty(t, parsed.Host)
		require.Empty(t, parsed.Fragment)
		expected := path
		if path[0] != '/' {
			expected = "/" + path
			require.Contains(t, u.String(), "file:///C:/")
		}
		require.Equal(t, expected, parsed.Path)
		require.Contains(t, u.String(), "%3F%23")
		require.Equal(t, "ro", parsed.Query().Get("mode"))
	}
	_, err := offlineSQLiteURL("//server/share/launcher.db")
	require.Error(t, err)
	_, err = offlineSQLiteURL("relative/launcher.db")
	require.Error(t, err)
}

func TestOfflineSQLiteRefusesInvalidDatabaseWithoutDeletingWAL(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "launcher.db"), []byte("not sqlite"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "launcher.db-wal"), []byte("retained evidence"), 0o600))
	store, err := OpenOfflineStore(home, true)
	if err == nil {
		_, _, err = store.LoadNamespaceState("ws", "default")
		require.Error(t, err)
		require.NoError(t, store.Close())
	}
	data, readErr := os.ReadFile(filepath.Join(home, "launcher.db-wal"))
	require.NoError(t, readErr)
	require.Equal(t, "retained evidence", string(data))
}
