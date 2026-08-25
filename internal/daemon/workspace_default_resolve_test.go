package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestDefaultWorkspaceWithNoNamespacesStillResolvesItsConfig pins the root
// cause of "the bundle repository dropdown is empty in the default workspace".
//
// The built-in `default` workspace is SYNTHETIC: syntheticDefaultWorkspace()
// carries no RepoURL, because the resolver substitutes DefaultBundlesRepo for
// an empty one. That made two guards mis-fire together:
//
//   - SwitchWorkspace skips the workspace resolve for default-repo targets on
//     purpose ("config then loads lazily via the namespace auto-load"), and
//   - reresolveActiveWorkspace bailed on `ws.RepoURL == ""`.
//
// So a default-repo workspace with NO namespaces had nothing that would ever
// resolve its config — the state migration leaves `default` in once the user's
// namespaces live in another workspace. The result was an empty, unfillable
// "Bundle repository" dropdown and a Quick Start that created a namespace with
// an empty bundle ref: seven third-party containers reporting RUNNING with
// none of the product in them.
func TestDefaultWorkspaceWithNoNamespacesStillResolvesItsConfig(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	resolved := &bundle.WorkspaceConfig{
		BundleRepos: []bundle.BundlesRepo{{ID: "community"}},
	}
	calls := 0

	d := testDaemon(t, store)
	// No namespace was ever loaded, so nothing populated workspaceConfig.
	d.activeNs = &activeNamespace{workspaceID: defaultWorkspaceID}
	d.wsCfgResolveFn = func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		calls++
		assert.Equal(t, defaultWorkspaceID, ws.ID)
		assert.Empty(t, ws.RepoURL, "the synthetic default carries no URL; the resolver supplies it")
		return resolved, nil
	}

	cfg, syncErr := d.activeWorkspaceConfigForRead()
	require.Empty(t, syncErr)
	require.Equal(t, 1, calls, "an unusable config must be re-resolved, not served as settled")
	require.NotNil(t, cfg)
	assert.Len(t, cfg.BundleRepos, 1,
		"without this the create dialog's bundle picker stays empty and Quick Start makes an infra-only namespace")

	// Once resolved, it is a settled answer: a thin config must not re-run git
	// I/O on every Welcome read.
	cfg2, syncErr2 := d.activeWorkspaceConfigForRead()
	require.Empty(t, syncErr2)
	assert.Equal(t, 1, calls, "a resolved config is served from cache")
	assert.Same(t, resolved, cfg2)
}

// A config that already works is served as-is — no repeated git I/O per read.
func TestUsableWorkspaceConfigIsNotReResolved(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := testDaemon(t, store)
	d.activeNs = &activeNamespace{
		workspaceID:     defaultWorkspaceID,
		workspaceConfig: &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{ID: "community"}}},
	}
	d.wsCfgResolveFn = func(storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		t.Fatal("a usable config must not trigger a re-resolve")
		return nil, nil
	}

	cfg, syncErr := d.activeWorkspaceConfigForRead()
	require.Empty(t, syncErr)
	assert.Len(t, cfg.BundleRepos, 1)
}
