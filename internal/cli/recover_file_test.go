package cli

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

const recoveryFixture = `{"status":"RUNNING","editedFiles":["app/x/props/application.yml","other"],"editedFileEdits":{"app/x/props/application.yml":{"format":"future","payload":{}},"other":{"format":"future"}},"dependencies":{"postgres":{"image":"postgres:17.5","volumeGen":3}},"manualStoppedApps":["ai"],"unknownFuture":{"keep":true}}`

func TestOfflineRecoveryPreservesStateAndBacksUpOnlyChosenEntry(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(map[bool]string{false: "server", true: "desktop"}[desktop], func(t *testing.T) {
			old := config.IsDesktopMode()
			config.SetDesktopMode(desktop)
			t.Cleanup(func() { config.SetDesktopMode(old) })
			t.Setenv("CITECK_HOME", t.TempDir())
			ws := ""
			ns := "default"
			cfg := "id: different-from-storage-key\n"
			if desktop {
				ws = "workspace"
				ns = "ns1"
				cfg = "name: legacy-without-id\n"
			}
			var store storage.Store
			var err error
			if desktop {
				store, err = storage.NewSQLiteStore(config.HomeDir())
			} else {
				store, err = storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
			}
			require.NoError(t, err)
			require.NoError(t, store.SaveNamespaceConfig(ws, ns, "test", cfg))
			require.NoError(t, store.SaveNamespaceState(ws, ns, "RUNNING", recoveryFixture))
			require.NoError(t, store.Close())
			store, err = storage.OpenOfflineStore(config.HomeDir(), desktop)
			require.NoError(t, err)
			defer store.Close()
			key := "app/x/props/application.yml"
			path := filepath.Join(config.ResolveVolumesBase(ws, ns), filepath.FromSlash(key))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("operator secret\n"), 0o644))
			result, err := recoverSavedFile(store, ws, ns, key, false)
			require.NoError(t, err)
			require.False(t, result.Applied)
			require.Empty(t, result.Backup)
			before, _, err := store.LoadNamespaceState(ws, ns)
			require.NoError(t, err)
			//nolint:testifylint // encoded-compare: exact bytes prove that preview/backup did not rewrite the saved state.
			require.Equal(t, []byte(recoveryFixture), []byte(before))
			require.FileExists(t, path)
			result, err = recoverSavedFile(store, ws, ns, key, true)
			require.NoError(t, err)
			require.True(t, result.Applied)
			relative, e := filepath.Rel(config.ResolveVolumesBase(ws, ns), result.Backup)
			require.NoError(t, e)
			require.True(t, relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)), "full state backup must be outside runtime mounts")
			matches, e := filepath.Glob(filepath.Join(filepath.Dir(path), ".citeck-quarantine-*", "state.json"))
			require.NoError(t, e)
			require.Empty(t, matches)
			require.NoFileExists(t, path)
			data, err := os.ReadFile(filepath.Join(result.Backup, "state.json"))
			require.NoError(t, err)
			//nolint:testifylint // encoded-compare: exact bytes prove that preview/backup did not rewrite the saved state.
			require.Equal(t, []byte(recoveryFixture), data)
			data, err = os.ReadFile(filepath.Join(result.Backup, "original"))
			require.NoError(t, err)
			require.Equal(t, "operator secret\n", string(data))
			if runtime.GOOS != "windows" {
				info, e := os.Stat(result.Backup)
				require.NoError(t, e)
				require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
				info, e = os.Stat(filepath.Join(result.Backup, "state.json"))
				require.NoError(t, e)
				require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			}
			after, _, err := store.LoadNamespaceState(ws, ns)
			require.NoError(t, err)
			var got map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(after), &got))
			require.JSONEq(t, `{"keep":true}`, string(got["unknownFuture"]))
			require.JSONEq(t, `["ai"]`, string(got["manualStoppedApps"]))
			require.JSONEq(t, `{"postgres":{"image":"postgres:17.5","volumeGen":3}}`, string(got["dependencies"]))
			require.JSONEq(t, `["other"]`, string(got["editedFiles"]))
			require.JSONEq(t, `{"other":{"format":"future"}}`, string(got["editedFileEdits"]))
		})
	}
}

func TestRecoveryRejectsWrongTargetUnknownEditAndPendingWork(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"editedFiles":7}`, `{"status":"STOPPED","editedFiles":["file"],"snapshotImportPending":true}`, `{"status":"STOPPED","editedFiles":["file"],"dependencyMigration":{}}`} {
		_, err := resetRecoveryState(raw, "file")
		require.Error(t, err)
	}
	_, err := resetRecoveryState(recoveryFixture, "unknown")
	require.Error(t, err)
}

func TestOfflineRecoveryExcludesDaemonAndOtherRecovery(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	t.Setenv("CITECK_RUN", cliSocketDir(t))
	release, err := lockOfflineRecovery()
	require.NoError(t, err)
	_, err = fsutil.AcquireProfileLock(config.HomeDir())
	require.ErrorIs(t, err, fsutil.ErrProfileBusy)
	_, err = lockOfflineRecovery()
	require.Error(t, err)
	release()
	lock, err := fsutil.AcquireProfileLock(config.HomeDir())
	require.NoError(t, err)
	require.NoError(t, lock.Close())
	// A daemon predating the profile lock is excluded by its existing socket.
	listener, err := net.Listen("unix", config.SocketPath())
	require.NoError(t, err)
	defer listener.Close()
	_, err = lockOfflineRecovery()
	require.Error(t, err)
}

type recoveryRefusedStore struct{ storage.Store }

func (recoveryRefusedStore) SaveNamespaceState(string, string, string, string) error {
	return errors.New("write refused")
}

func TestOfflineRecoverySaveFailureRestoresOriginal(t *testing.T) {
	old := config.IsDesktopMode()
	config.SetDesktopMode(false)
	defer config.SetDesktopMode(old)
	t.Setenv("CITECK_HOME", t.TempDir())
	store, err := storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceConfig("", "default", "test", "id: ns1\n"))
	require.NoError(t, store.SaveNamespaceState("", "default", "RUNNING", recoveryFixture))
	key := "app/x/props/application.yml"
	path := filepath.Join(config.ResolveVolumesBase("", "default"), filepath.FromSlash(key))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	result, err := recoverSavedFile(recoveryRefusedStore{store}, "", "default", key, true)
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.FileExists(t, filepath.Join(result.Backup, "state.json"))
	after, _, err := store.LoadNamespaceState("", "default")
	require.NoError(t, err)
	//nolint:testifylint // encoded-compare: exact bytes prove that preview/backup did not rewrite the saved state.
	require.Equal(t, []byte(recoveryFixture), []byte(after))
}
