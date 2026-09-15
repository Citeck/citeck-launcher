package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The key is the bundle author's statement about which launcher can run this
// file. It is read from the YAML TEXT for the same reason every image tag is:
// in a decoded map an unquoted 2.10 is already float64(2.1), and a floor
// nobody wrote is worse than no floor at all.
func TestParseBundleFile_MinLauncherVersionIsReadAsText(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion: 2.10
EcosModelApp:
  image:
    repository: core/ecos-model
    tag: "1.0"
`)
	assert.Equal(t, "2.10", def.MinLauncherVersion,
		"2.1 is a floor nobody wrote")
	assert.Contains(t, def.Applications, "EcosModelApp",
		"the key is not an application and must not cost the bundle its apps")
	assert.NotContains(t, def.Applications, "minLauncherVersion")
}

func TestParseBundleFile_MinLauncherVersionQuoted(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion: "2.13.0"
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Equal(t, "2.13.0", def.MinLauncherVersion)
}

// Absent and blank both mean "no requirement". Blank has to be spelled out:
// the comparator treats an unparseable floor as newer than everything, so a
// blank value left as-is would refuse every launcher.
func TestParseBundleFile_MinLauncherVersionAbsentOrBlank(t *testing.T) {
	absent := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Empty(t, absent.MinLauncherVersion)

	blank := parseTestBundle(t, `
minLauncherVersion: "   "
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Empty(t, blank.MinLauncherVersion, "a blank floor is no floor")
}

// A shape that is not a scalar is not a version. It costs the key, never the
// bundle — the same price an unreadable dependency entry pays.
func TestParseBundleFile_MinLauncherVersionWrongShape(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion:
  - 2.13.0
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Empty(t, def.MinLauncherVersion)
	assert.Contains(t, def.Applications, "EcosModelApp")
}
