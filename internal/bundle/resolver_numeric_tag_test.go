package bundle

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An unquoted numeric tag is a spelling bundle authors reach for by accident —
// YAML turns `tag: 17.10` into a float long before anything here looks at it,
// and "17.10" comes back as "17.1" if it comes back at all. The bundle's own
// `dependencies:` section already reads its tags from the YAML text for exactly
// this reason; every OTHER entry used to read them from a generic map, where the
// float failed a string type-assertion, the tag came out empty, and the
// APPLICATION DISAPPEARED from the bundle — no image, no container, no log line.
func TestParseBundleFile_UnquotedNumericTagKeepsItsText(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image:
    repository: core/ecos-model
    tag: 17.10
EcosProcessApp:
  image:
    repository: core/ecos-process
    tag: 18
`)
	require.Contains(t, def.Applications, "EcosModelApp",
		"an app with an unquoted numeric tag must not vanish from the bundle")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosModelApp"].Image,
		"17.10 is the text the author wrote; 17.1 is a version nobody wrote")
	require.Contains(t, def.Applications, "EcosProcessApp")
	assert.Equal(t, "nexus.citeck.ru/ecos-process:18", def.Applications["EcosProcessApp"].Image)
}

// The ecos: scope is a second path into the same reader, and a Helm-style
// bundle puts most core apps there.
func TestParseBundleFile_UnquotedNumericTagUnderEcosScope(t *testing.T) {
	def := parseTestBundle(t, `
ecos:
  EcosModelApp:
    image:
      repository: core/ecos-model
      tag: 17.10
`)
	require.Contains(t, def.Applications, "EcosModelApp")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosModelApp"].Image)
}

// ecosAppsImages is a third path, and there the empty tag is quieter still: the
// init container is simply dropped from eapps and the stand comes up missing an
// artifact nobody asked about.
func TestParseBundleFile_UnquotedNumericTagInEcosAppsImages(t *testing.T) {
	def := parseTestBundle(t, `
eapps:
  image:
    repository: core/ecos-apps
    tag: 1.0
  ecosAppsImages:
    - repository: core/ecos-data-app
      tag: 2.10
    - repository: core/ecos-model-app
      tag: 3
`)
	require.Contains(t, def.Applications, "eapps")
	assert.Equal(t, "nexus.citeck.ru/ecos-apps:1.0", def.Applications["eapps"].Image)
	require.Len(t, def.CiteckApps, 2)
	assert.Equal(t, "nexus.citeck.ru/ecos-data-app:2.10", def.CiteckApps[0].Image)
	assert.Equal(t, "nexus.citeck.ru/ecos-model-app:3", def.CiteckApps[1].Image)
}

// Reading tags from the YAML text means walking nodes, and a node walk is the
// one place where YAML's own indirection — anchors, aliases and merge keys —
// stops being free. Real bundles are generated from Helm charts, so these three
// guards stand for the shapes a generator can emit.
func TestParseBundleFile_ImageThroughAnAnchoredMerge(t *testing.T) {
	def := parseTestBundle(t, `
.base: &base
  image:
    repository: core/ecos-model
    tag: 17.10
EcosModelApp:
  <<: *base
EcosProcessApp: *base
`)
	require.Contains(t, def.Applications, "EcosModelApp", "a merge key must still name the image")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosModelApp"].Image)
	require.Contains(t, def.Applications, "EcosProcessApp", "an aliased entry must still name the image")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosProcessApp"].Image)
}

// The list form outside `dependencies:` keeps taking its FIRST element: nothing
// out here can walk a ladder. Guards the rule against the node walk.
func TestParseBundleFile_TopLevelImageListStillTakesTheFirstRung(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image:
    - repository: core/ecos-model
      tag: 17.10
    - repository: core/ecos-model
      tag: 18.6
`)
	require.Contains(t, def.Applications, "EcosModelApp")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosModelApp"].Image)
}

// A whole app list can arrive through a merge key, and then YAML's own
// precedence applies: what the mapping writes itself beats what it inherits.
func TestParseBundleFile_MergedAppListLosesToAnExplicitEntry(t *testing.T) {
	def := parseTestBundle(t, `
.base: &base
  EcosModelApp:
    image:
      repository: core/ecos-model
      tag: 1.10
  EcosProcessApp:
    image:
      repository: core/ecos-process
      tag: 1.10
ecos:
  <<: *base
  EcosModelApp:
    image:
      repository: core/ecos-model
      tag: 2.20
`)
	assert.Equal(t, "nexus.citeck.ru/ecos-model:2.20", def.Applications["EcosModelApp"].Image,
		"the entry written in the mapping beats the one merged in")
	assert.Equal(t, "nexus.citeck.ru/ecos-process:1.10", def.Applications["EcosProcessApp"].Image,
		"an app that only the merge brings in must still be there")
}

// An anchored image is a node yaml.v3 hands through verbatim when the
// destination is a yaml.Node, so an unresolved alias would read as an image
// nobody named — and the app would vanish exactly as an empty tag makes it.
func TestParseBundleFile_AnchoredImageValue(t *testing.T) {
	def := parseTestBundle(t, `
.base: &img
  repository: core/ecos-model
  tag: 17.10
EcosModelApp:
  image: *img
`)
	require.Contains(t, def.Applications, "EcosModelApp")
	assert.Equal(t, "nexus.citeck.ru/ecos-model:17.10", def.Applications["EcosModelApp"].Image)
}

// An application that names an image the launcher cannot read leaves the bundle
// exactly as quietly as the empty-tag case did. Silence is the wrong price:
// nothing on the stand says the app was even meant to be there.
func TestParseBundleFile_AnUnreadableApplicationImageIsLogged(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
EcosModelApp:
  image:
    repository: core/ecos-model
EcosProxyApp:
  image: core/ecos-proxy:1.0
imageRepos:
  core:
    url: nexus.citeck.ru
`), 0o600))

	def, err := parseBundleFile(path, "test", nil, map[string]string{"core": "nexus.citeck.ru"}, logger)
	require.NoError(t, err)
	assert.NotContains(t, def.Applications, "EcosModelApp")
	assert.Contains(t, buf.String(), "EcosModelApp", "the dropped application must be named in the log")
	assert.NotContains(t, buf.String(), "EcosProxyApp", "an entry that was read is not a finding")
	assert.NotContains(t, buf.String(), "imageRepos",
		"a key that names no image at all is not an application and not a finding")
}
