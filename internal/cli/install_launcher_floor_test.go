package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// writeInstallBundles lays out a bundles directory: version key → the floor it
// declares ("" for none).
func writeInstallBundles(t *testing.T, floors map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for key, floor := range floors {
		body := "EcosModelApp:\n  image: core/ecos-model:1.0\n"
		if floor != "" {
			body = "minLauncherVersion: \"" + floor + "\"\n" + body
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, key+".yaml"), []byte(body), 0o600))
	}
	return dir
}

// citeck install writes the namespace config ITSELF (fsutil.AtomicWriteFile),
// bypassing the daemon, so the floor has to be checked here or the CLI can
// create exactly the namespace the daemon's gate exists to prevent.
func TestCheckReleaseFloor_RefusesABundleAboveTheFloor(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": "9.9.9"})

	err := checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "9.9.9", "the message must name the version to update to")
	assert.Contains(t, err.Error(), "2.12.2", "and the version the operator is on")
}

func TestCheckReleaseFloor_AcceptsABundleAtTheFloor(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": "2.12.2"})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2"))
}

func TestCheckReleaseFloor_NoFloorNoRefusal(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": ""})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2"))
}

// A version whose file is not there is not a refusal — the picker could only
// have offered it from a listing, and a missing file is the repo's problem, not
// the launcher's floor.
func TestCheckReleaseFloor_MissingFileIsNotARefusal(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": ""})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2099.1"}, "2.12.2"))
}
