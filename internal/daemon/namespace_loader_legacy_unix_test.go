//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

func TestLoadNamespaceLegacyFilesFailClosedBeforeRuntimeHandoff(t *testing.T) {
	for _, kind := range []string{"symlink", "parent-symlink", "fifo", "oversized", "missing", "unknown", "valid", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			// All Docker requests terminate at a fake HTTP endpoint, never the host engine.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "not found", http.StatusNotFound) }))
			defer srv.Close()
			t.Setenv("DOCKER_HOST", srv.URL)
			t.Setenv("DOCKER_API_VERSION", "1.54")
			d, _ := newContentStorageDaemon(t)
			require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, namespace.ContentStorageDB)))
			base := config.ResolveVolumesBase("wsMain", "ns1")
			key := "app/content/props/application-launcher.yml"
			path := filepath.Join(base, filepath.FromSlash(key))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			outside := filepath.Join(t.TempDir(), "application-launcher.yml")
			require.NoError(t, os.WriteFile(outside, []byte("outside: private\n"), 0o600))
			state := namespace.NsPersistedState{Status: namespace.NsStatusStopped, EditedFiles: []string{key}}
			switch kind {
			case "symlink":
				require.NoError(t, os.Symlink(outside, path))
			case "parent-symlink":
				require.NoError(t, os.Remove(filepath.Dir(path)))
				require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Dir(path)))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(path, 0o600))
			case "oversized":
				require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0o600))
			case "unknown":
				key = "app/content/props/application-no-longer-generated.yml"
				path = filepath.Join(base, filepath.FromSlash(key))
				state.EditedFiles = []string{key}
				require.NoError(t, os.WriteFile(path, []byte("operator: retained\n"), 0o600))
			case "valid", "mixed":
				require.NoError(t, os.WriteFile(path, []byte("operator: retained\n"), 0o600))
			}
			if kind == "mixed" {
				other := "app/content/props/application.yml"
				// An existing delta may coexist with legacy flags; neither may be lost.
				edit, e := namespace.MakeFileEdit("application.yml", []byte("{}"), []byte("operator: delta\n"))
				require.NoError(t, e)
				state.EditedFileEdits = map[string]namespace.FileEdit{other: edit}
			}
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			require.NoError(t, d.store.SaveNamespaceState("wsMain", "ns1", string(state.Status), string(raw)))
			before, statErr := os.Lstat(path)
			if kind != "missing" {
				require.NoError(t, statErr)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			started := time.Now()
			loaded, err := loadNamespace(loadNamespaceInput{Ctx: ctx, Store: d.store, SecretService: d.secretService, WorkspaceID: "wsMain", NamespaceID: "ns1", Offline: true, Desktop: true})
			require.Less(t, time.Since(started), 3*time.Second, "special files must fail without blocking")
			if kind == "valid" || kind == "mixed" {
				require.NoError(t, err)
				require.NotNil(t, loaded.Runtime)
				defer loaded.Runtime.Shutdown()
				if loaded.DockerClient != nil {
					defer loaded.DockerClient.Close()
				}
				require.True(t, loaded.Runtime.IsFileEdited(key))
				if kind == "mixed" {
					require.True(t, loaded.Runtime.IsFileEdited("app/content/props/application.yml"))
				}
			} else {
				require.Error(t, err)
				require.Nil(t, loaded, "no runtime may be handed off after migration failure")
			}
			saved, ok, err := d.store.LoadNamespaceState("wsMain", "ns1")
			require.NoError(t, err)
			require.True(t, ok)
			require.JSONEq(t, string(raw), saved, "migration failure must not drop persisted legacy flags")
			if kind != "missing" {
				after, e := os.Lstat(path)
				require.NoError(t, e)
				require.True(t, os.SameFile(before, after), "legacy entry must not be replaced")
			} else {
				_, e := os.Lstat(path)
				require.True(t, os.IsNotExist(e))
			}
			data, err := os.ReadFile(outside)
			require.NoError(t, err)
			require.Equal(t, "outside: private\n", string(data))
			info, err := os.Stat(outside)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})
	}
}
