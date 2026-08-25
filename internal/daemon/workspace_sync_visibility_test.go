package daemon

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// TestDefaultWorkspaceRepoFailureSurfacesWhenItLeavesNothingUsable pins the
// root cause of "the bundle repository dropdown is empty and Quick Start makes
// an infra-only namespace".
//
// The built-in Citeck workspace repo fails GRACEFULLY by design — booting must
// not depend on reaching github.com — so Resolver.WorkspaceSyncError reports
// nothing for it, and every guard built on wsSyncError (quick starts, workspace
// snapshots) was inert for the `default` workspace. A user whose network
// reaches gitlab.citeck.ru but not github.com therefore got a workspace config
// with no bundleRepos and NO error anywhere: an unfillable create dialog, and a
// Quick Start namespace with an empty bundle ref that came up as seven
// third-party containers reporting RUNNING with none of the product in it.
//
// The graceful path is kept for the case it was written for — a fallback that
// is still usable — and speaks up only when it left nothing to work with.
func TestDefaultWorkspaceRepoFailureSurfacesWhenItLeavesNothingUsable(t *testing.T) {
	syncErr := errors.New("clone https://github.com/Citeck/launcher-workspace.git: dial tcp: i/o timeout")
	usable := &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{ID: "community"}}}

	t.Run("default repo, unusable config → surfaced", func(t *testing.T) {
		r := bundle.NewResolver(t.TempDir())
		r.SetWorkspaceSyncErrForTest(syncErr)
		assert.Equal(t, syncErr.Error(), workspaceSyncErrorString(r, &bundle.WorkspaceConfig{}))
		assert.Equal(t, syncErr.Error(), workspaceSyncErrorString(r, nil))
	})

	t.Run("default repo, usable fallback config → still graceful", func(t *testing.T) {
		r := bundle.NewResolver(t.TempDir())
		r.SetWorkspaceSyncErrForTest(syncErr)
		assert.Empty(t, workspaceSyncErrorString(r, usable),
			"a cached/bundled config that still works must not start 502-ing the Welcome endpoints")
	})

	t.Run("no failure → nothing to report", func(t *testing.T) {
		r := bundle.NewResolver(t.TempDir())
		assert.Empty(t, workspaceSyncErrorString(r, &bundle.WorkspaceConfig{}))
	})

	t.Run("custom repo keeps surfacing regardless", func(t *testing.T) {
		r := bundle.NewResolver(t.TempDir()).
			WithWorkspaceRepo(bundle.WorkspaceRepoOpts{URL: "https://gitlab.example.com/ws.git"})
		r.SetWorkspaceSyncErrForTest(syncErr)
		assert.Equal(t, syncErr.Error(), workspaceSyncErrorString(r, usable))
	})
}
