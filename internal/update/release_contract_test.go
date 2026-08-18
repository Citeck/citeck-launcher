package update_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A staged payload is not merely a downloaded file — it is the path the desktop
// supervisor hands to exec on the next spawn. On Windows os/exec resolves an
// absolute path through lookExtensions → findExecutable, which with a non-empty
// PATHEXT only ever tries "<name><ext>" and NEVER the extension-less file. So a
// payload written as plain "citeck" downloads, verifies its sha256 and signature,
// stages and is selected — and then the wrapper cannot start it at all
// (exec.ErrNotFound). The health gate cannot save that either: nothing about the
// payload is unhealthy, it is unreachable.
func TestDaemonBinaryNameCarriesTheWindowsExeSuffix(t *testing.T) {
	assert.Equal(t, "citeck.exe", update.DaemonBinaryName("windows"))
	for _, goos := range []string{"linux", "darwin"} {
		assert.Equal(t, "citeck", update.DaemonBinaryName(goos),
			"%s must not get an extension", goos)
	}
}

// TestReleaseMatrixBuildsAPayloadForEveryPlatformTheUpdaterAsksFor is the gate
// that keeps auto-update honest across the artifact boundary. The updater asks
// GitHub for citeck_<ver>_<GOOS>_<GOARCH>.tar.gz on whatever platform it happens
// to run; the release workflow decides which of those actually exist. A platform
// present in the desktop installers but missing from this matrix ships a
// launcher that can never update itself — silently, since a missing asset is
// just a 404 at staging time on someone else's machine, months later and on an
// OS no CI job here runs.
func TestReleaseMatrixBuildsAPayloadForEveryPlatformTheUpdaterAsksFor(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "release-go.yml")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative path
	if os.IsNotExist(err) {
		t.Skipf("%s not present in this checkout", path)
	}
	require.NoError(t, err)

	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct {
						OS   string `yaml:"os"`
						Arch string `yaml:"arch"`
					} `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &wf))

	const version = "9.9.9"
	built := map[string]bool{}
	for _, e := range wf.Jobs["build"].Strategy.Matrix.Include {
		require.NotEmpty(t, e.OS, "release matrix entry without an os")
		require.NotEmpty(t, e.Arch, "release matrix entry without an arch")
		built[update.PayloadAssetName(version, e.OS, e.Arch)] = true
	}

	// Every platform we ship a desktop installer for — that is the set whose
	// installed launchers call Stage.
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			want := update.PayloadAssetName(version, goos, goarch)
			assert.Truef(t, built[want],
				"the release build matrix publishes no %s — auto-update on %s/%s would 404 at staging",
				want, goos, goarch)
		}
	}
	assert.Len(t, built, 6, "unexpected extra entries in the release build matrix: %v", keys(built))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, fmt.Sprintf("%q", k))
	}
	return out
}

// TestPayloadAssetNameMatchesWhatTheReleaseScriptPublishes closes the other half
// of the contract. The name the updater REQUESTS and the name the release
// actually PUBLISHES are produced by two different files in two different
// languages — PayloadAssetName here and the TARBALL= assignment in
// packaging/release-server.sh — with nothing but convention holding them
// together. The matrix test above only proves a platform is built; if either
// side of the NAME drifts (a rename in the script, a changed format string
// here) every platform 404s at staging while both files still look right on
// their own, and the first symptom is a user's launcher that silently stops
// updating.
func TestPayloadAssetNameMatchesWhatTheReleaseScriptPublishes(t *testing.T) {
	path := filepath.Join("..", "..", "packaging", "release-server.sh")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative path
	if os.IsNotExist(err) {
		t.Skipf("%s not present in this checkout", path)
	}
	require.NoError(t, err)

	m := regexp.MustCompile(`(?m)^TARBALL="([^"]+)"`).FindSubmatch(raw)
	require.NotNil(t, m, "no TARBALL= assignment found in %s", path)

	// Expand exactly the variables the script has in scope at that line.
	got := strings.NewReplacer(
		"${VERSION}", "9.9.9", "${GOOS}", "darwin", "${GOARCH}", "arm64",
	).Replace(string(m[1]))
	assert.NotContains(t, got, "${",
		"unexpanded variable in %q — the script names the tarball from inputs this test does not model", got)
	assert.Equal(t, update.PayloadAssetName("9.9.9", "darwin", "arm64"), got,
		"the updater asks for a different file than the release publishes")
}
