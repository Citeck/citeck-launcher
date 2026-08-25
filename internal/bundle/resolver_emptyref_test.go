package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveEmptyRefKeepsTheWorkspaceConfig pins the branch that turned one
// broken namespace into a broken launcher.
//
// A namespace with no bundle ref is a broken config, not a reason to forget the
// workspace. Resolve used to answer it with `Workspace: &WorkspaceConfig{}`,
// and the daemon assigns that verbatim (`a.workspaceConfig =
// resolveResult.Workspace`, `wsCfg = resolveResult.Workspace`). The real
// workspace config was replaced by a blank one, which then had no bundleRepos,
// no imageRepos and no namespace templates — so the NEXT namespace created got
// an empty bundle ref too (applyDefaultTemplate's fallback needs BundleRepos),
// the secrets start gate saw no auth-required registries, and registry auth
// resolved nothing. Self-propagating, and silent: an empty ref is not an error.
//
// The resolve-FAILURE path in namespace_loader.go preserves the workspace for
// exactly this reason; the empty-ref path was the one that did not.
func TestResolveEmptyRefKeepsTheWorkspaceConfig(t *testing.T) {
	dataDir := t.TempDir()
	// A workspace ZIP import (repo/ without .git) — priority 1 in
	// resolveWorkspace, so no network is involved.
	repoDir := filepath.Join(dataDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"), []byte(`
imageRepos:
  - id: ent
    url: enterprise-registry.citeck.ru
    authType: BASIC
bundleRepos:
  - id: community
    name: Community Bundles
    url: https://github.com/Citeck/launcher-workspace.git
    branch: main
    path: community
`), 0o600))

	res, err := NewResolver(dataDir).Resolve(Ref{})
	require.NoError(t, err, "an empty ref stays a non-error: the bundle is empty, nothing failed")
	require.NotNil(t, res.Workspace)

	assert.True(t, res.Bundle.IsEmpty(), "no ref, no bundle")
	assert.Len(t, res.Workspace.BundleRepos, 1,
		"the workspace config must survive — a blank one poisons every later create")
	assert.Len(t, res.Workspace.ImageRepos, 1,
		"losing imageRepos silently disables the registry auth and the secrets start gate")
}
