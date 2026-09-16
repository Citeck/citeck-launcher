package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeWorkspaceClone lays out a workspace repo clone the way the shipped
// public workspace is laid out: the workspace file at the root and each
// declared bundle repo as a directory beside it.
func writeWorkspaceClone(t *testing.T, dataDir string, bundlePaths map[string][]string) string {
	t.Helper()
	wsRepoDir := filepath.Join(dataDir, "bundles", "workspace")
	require.NoError(t, os.MkdirAll(wsRepoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsRepoDir, "workspace-v1.yml"),
		[]byte("webapps: []\n"), 0o644))
	for path, versions := range bundlePaths {
		dir := filepath.Join(wsRepoDir, path)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for _, v := range versions {
			require.NoError(t, os.WriteFile(filepath.Join(dir, v+".yaml"),
				[]byte("emodel:\n  image: citeck/emodel:1.0.0\n"), 0o644))
		}
	}
	return wsRepoDir
}

// SyncBundleRepo must sync the directory Resolve READS. In the shipped layout
// every bundleRepos entry is a path inside the workspace repo itself, so
// Resolve reads the bundles out of the workspace clone and never opens
// bundles/<id>. A sync that cloned into bundles/<id> reported success while
// the version the operator pressed ↻ for stayed invisible.
//
// The assertion is on the returned directory CONTENTS rather than its name:
// the caller's whole purpose is to have the asked-for versions on disk, and
// the old answer (bundles/community) contains none of them.
func TestSyncBundleRepoSyncsTheCloneResolveReads(t *testing.T) {
	dataDir := t.TempDir()
	wsRepoDir := writeWorkspaceClone(t, dataDir, map[string][]string{"community": {"2026.2", "2026.3"}})

	cfg := &WorkspaceConfig{BundleRepos: []BundlesRepo{
		{ID: "community", URL: "https://github.com/Citeck/launcher-workspace.git", Path: "community"},
	}}
	r := NewResolver(dataDir).
		WithWorkspaceRepo(WorkspaceRepoOpts{URL: "https://github.com/Citeck/launcher-workspace.git", Branch: "main"})
	r.SetOffline(true) // the decision under test is which directory, not git

	dir, err := r.SyncBundleRepo(cfg, "community")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wsRepoDir, "community"), dir)
	assert.FileExists(t, filepath.Join(dir, "2026.3.yaml"),
		"the synced directory must be the one holding the bundles — bundles/<id> holds none of them")
	assert.NoDirExists(t, filepath.Join(dataDir, "bundles", "community"),
		"a second clone of the workspace repo per declared repo id is disk nobody reads")
}

// The other half of the same decision: a bundle repo the workspace clone does
// NOT carry is a repo of its own, and cloning it into bundles/<id> is exactly
// right. Without this, "always use the workspace clone" would pass the test
// above and break every workspace whose bundles live elsewhere.
func TestSyncBundleRepoStillClonesARepoTheWorkspaceDoesNotCarry(t *testing.T) {
	dataDir := t.TempDir()
	writeWorkspaceClone(t, dataDir, map[string][]string{"community": {"2026.2"}})

	cfg := &WorkspaceConfig{BundleRepos: []BundlesRepo{
		{ID: "enterprise", URL: "https://gitlab.example.com/citeck/enterprise-bundles.git", Path: "enterprise"},
	}}
	r := NewResolver(dataDir).
		WithWorkspaceRepo(WorkspaceRepoOpts{URL: "https://github.com/Citeck/launcher-workspace.git", Branch: "main"})
	r.SetOffline(true)

	dir, err := r.SyncBundleRepo(cfg, "enterprise")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dataDir, "bundles", "enterprise"), dir)
}

// The trap behind the Force Update defect, pinned where it lives: a zero
// PullPeriod on the options means "not configured" and resolves to the
// default throttle. Only WithForcePull bypasses it. Anyone tempted to make
// the zero mean "force" should read this first — buildWorkspaceRepoOpts
// leaves the field zero for every workspace that configures no period, so
// that meaning would turn every ordinary resolve into an unconditional pull.
func TestAZeroPullPeriodIsNotAForcePull(t *testing.T) {
	r := NewResolver(t.TempDir()).WithWorkspaceRepo(WorkspaceRepoOpts{
		URL: "https://github.com/Citeck/launcher-workspace.git", Branch: "main", PullPeriod: 0,
	})

	_, _, pullPeriod, _ := r.workspaceRepoSettings()
	assert.Equal(t, defaultPullPeriod, pullPeriod,
		"a zero PullPeriod is 'not configured', not 'force'")

	_, _, forced, _ := r.WithForcePull().workspaceRepoSettings()
	assert.Zero(t, forced, "WithForcePull is the one that bypasses the throttle")
}
