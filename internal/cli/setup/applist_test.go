package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// writeSetupWorkspace lays out a manual-import workspace ({CITECK_HOME}/data/repo,
// no .git — the resolveWorkspace priority-1 path, same layout
// wsConfigTestMux/writeFlooredWorkspace use elsewhere) whose "community"
// bundleRepos entry IS the workspace repo, with two versions: 2026.2 (no
// floor, app "AppOld") and 2026.3 (floor 2.13.0, app "AppNew").
func writeSetupWorkspace(t *testing.T) {
	t.Helper()
	repoDir := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n    name: Community\n    path: community\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "community", "2026.2.yaml"),
		[]byte("AppOld:\n  image: core/app-old:1.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "community", "2026.3.yaml"),
		[]byte("minLauncherVersion: \"2.13.0\"\nAppNew:\n  image: core/app-new:1.0\n"), 0o600))
}

// A namespace stored as LATEST must resolve, in `citeck setup`, to the same
// bundle the running daemon would pick — the newest bundle THIS launcher can
// run, not merely the newest one that exists. Without
// resolveAppList's .WithLauncherVersion(launcherVersion), the resolver's
// launcherVersion stays "" and LatestRunnableBundle short-circuits to the
// newest EXISTING version regardless of any floor, so a namespace running the
// held-back 2026.2 bundle would have its settings menu built from 2026.3's
// (unreachable) app list instead.
func TestResolveAppList_LatestHonorsTheLauncherFloor(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	writeSetupWorkspace(t)

	nsCfg := &namespace.Config{BundleRef: bundle.Ref{Repo: "community", Key: "LATEST"}}

	apps := resolveAppList("2.12.2", nsCfg)

	assert.Equal(t, []string{"AppOld"}, apps,
		"2026.3 needs launcher 2.13.0 and must be skipped for a 2.12.2 launcher")
}

// The mirror case: a launcher new enough for the newest bundle gets it.
func TestResolveAppList_LatestTakesTheNewestWhenItFits(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	writeSetupWorkspace(t)

	nsCfg := &namespace.Config{BundleRef: bundle.Ref{Repo: "community", Key: "LATEST"}}

	apps := resolveAppList("2.13.0", nsCfg)

	assert.Equal(t, []string{"AppNew"}, apps)
}
