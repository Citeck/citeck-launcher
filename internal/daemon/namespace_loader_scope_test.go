package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/storage"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamespaceLoadRefreshesWorkspaceAfterVersionChange(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		name := "server"
		if desktop {
			name = "desktop"
		}
		t.Run(name, func(t *testing.T) {
			config.SetDesktopMode(desktop)
			t.Cleanup(config.ResetDesktopMode)
			t.Setenv("CITECK_HOME", t.TempDir())
			upstream := t.TempDir()
			repo, err := gogit.PlainInit(upstream, false)
			require.NoError(t, err)
			wt, err := repo.Worktree()
			require.NoError(t, err)
			commit := func(filename, value string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(upstream, filename), []byte("quickStartVariants:\n  - name: "+value+"\n"), 0o600))
				_, addErr := wt.Add(filename)
				require.NoError(t, addErr)
				_, commitErr := wt.Commit(value, &gogit.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com"}})
				require.NoError(t, commitErr)
			}
			commit("workspace-v1.yml", "Old")
			store, err := storage.NewSQLiteStore(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			secrets, err := storage.NewSecretService(store)
			require.NoError(t, err)
			const wsID = "version-test"
			require.NoError(t, store.SaveWorkspace(storage.WorkspaceDto{ID: wsID, Name: "Version test", RepoURL: upstream, RepoBranch: "master", RepoPullPeriod: "PT2H"}))
			resolver := bundle.NewResolver(config.BundlesDataDir(wsID)).WithWorkspaceRepo(lookupWorkspaceRepoOpts(store, nil, wsID)).WithLauncherVersion("2.16.1")
			require.Equal(t, "Old", resolver.ResolveWorkspaceOnly().QuickStartVariants[0].Name)
			commit("workspace-v2.yml", "New")
			input := loadNamespaceInput{Store: store, SecretService: secrets, WorkspaceID: wsID, NamespaceID: "missing", Offline: true, Desktop: desktop, LauncherVersion: "2.16.2"}
			loaded, err := loadNamespace(input)
			require.NoError(t, err)
			assert.Equal(t, "Old", loaded.WorkspaceConfig.QuickStartVariants[0].Name, "--offline must not refresh")
			input.Offline = false
			loaded, err = loadNamespace(input)
			require.NoError(t, err)
			assert.Equal(t, "New", loaded.WorkspaceConfig.QuickStartVariants[0].Name, "new version refreshes before loading the namespace")
			commit("workspace-v2.yml", "Later")
			loaded, err = loadNamespace(input)
			require.NoError(t, err)
			assert.Equal(t, "New", loaded.WorkspaceConfig.QuickStartVariants[0].Name, "same version must keep the normal pull policy")
		})
	}
}

// TestDockerClientScoped pins the invariant enforcer used by loadNamespace: a
// client is reused only when it is already scoped to exactly (workspace, ns);
// any mismatch (or nil) forces a rebuild, which is what makes the
// wrong-namespace client bug impossible regardless of the caller.
func TestDockerClientScoped(t *testing.T) {
	c, err := docker.NewClient("default", "ns1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if c.Namespace() != "ns1" || c.Workspace() != "default" {
		t.Fatalf("getters wrong: ws=%q ns=%q", c.Workspace(), c.Namespace())
	}

	cases := []struct {
		name      string
		dc        *docker.Client
		ws, ns    string
		wantMatch bool
	}{
		{"exact match → reuse", c, "default", "ns1", true},
		{"different namespace → rebuild", c, "default", "ns2", false},
		{"different workspace → rebuild", c, "other", "ns1", false},
		{"nil client → rebuild", nil, "default", "ns1", false},
	}
	for _, tc := range cases {
		if got := dockerClientScoped(tc.dc, tc.ws, tc.ns); got != tc.wantMatch {
			t.Errorf("%s: dockerClientScoped = %v, want %v", tc.name, got, tc.wantMatch)
		}
	}
}
