//go:build !windows

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryQuarantinesUnsafeLeafWithoutReadingOrFollowing(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "missing", "parent-symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			old := config.IsDesktopMode()
			config.SetDesktopMode(false)
			defer config.SetDesktopMode(old)
			t.Setenv("CITECK_HOME", t.TempDir())
			store, err := storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
			require.NoError(t, err)
			defer store.Close()
			require.NoError(t, store.SaveNamespaceConfig("", "default", "test", "id: ns1\n"))
			require.NoError(t, store.SaveNamespaceState("", "default", "RUNNING", recoveryFixture))
			key := "app/x/props/application.yml"
			path := filepath.Join(config.ResolveVolumesBase("", "default"), filepath.FromSlash(key))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			outside := filepath.Join(t.TempDir(), "outside")
			require.NoError(t, os.WriteFile(outside, []byte("private"), 0o600))
			switch kind {
			case "symlink":
				require.NoError(t, os.Symlink(outside, path))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(path, 0o600))
			case "parent-symlink":
				require.NoError(t, os.Remove(filepath.Dir(path)))
				require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Dir(path)))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o755))
			}
			result, err := recoverSavedFile(store, "", "default", key, true)
			if kind == "parent-symlink" || kind == "directory" {
				require.Error(t, err)
				after, _, e := store.LoadNamespaceState("", "default")
				require.NoError(t, e)
				//nolint:testifylint // encoded-compare: exact bytes prove that preview/backup did not rewrite the saved state.
				require.Equal(t, []byte(recoveryFixture), []byte(after))
			} else {
				require.NoError(t, err)
				require.True(t, result.Applied)
				if kind != "missing" {
					manifest, e := os.ReadFile(filepath.Join(result.Backup, "manifest.json"))
					require.NoError(t, e)
					var receipt map[string]string
					require.NoError(t, json.Unmarshal(manifest, &receipt))
					info, e := os.Lstat(receipt["originalLocation"])
					require.NoError(t, e)
					require.False(t, info.Mode().IsRegular())
				}
			}
			data, e := os.ReadFile(outside)
			require.NoError(t, e)
			require.Equal(t, "private", string(data))
		})
	}
}
