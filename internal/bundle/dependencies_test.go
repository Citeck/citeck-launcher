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

// writeBundle writes a bundle YAML to a temp dir and parses it with the
// imageRepos map every real workspace has.
func parseTestBundle(t *testing.T, yml string) *Def {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yml), 0o600))
	def, err := parseBundleFile(path, "test", nil, map[string]string{"core": "nexus.citeck.ru"}, nil)
	require.NoError(t, err)
	return def
}

// The owner's own spelling of the new section: a plain image string. It is the
// form a bundle author reaches for first, so it must work.
func TestParseBundleFile_DependenciesAcceptAPlainImageString(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  postgres:
    image: postgres:17.11
  rabbitmq:
    image: rabbitmq:4.2.9-management
`)
	assert.Equal(t, "postgres:17.11", def.Dependencies["postgres"].Image)
	assert.Equal(t, "rabbitmq:4.2.9-management", def.Dependencies["rabbitmq"].Image)
	assert.NotContains(t, def.Applications, "postgres",
		"a dependency must NOT land in Applications — that is the map an old launcher would read")
}

// The {repository, tag} map form is how every existing bundle entry names an
// image, so a bundle author's habit has to work in the new section too.
func TestParseBundleFile_DependenciesAcceptTheRepositoryTagForm(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  postgres:
    image:
      repository: postgres
      tag: "17.11"
`)
	assert.Equal(t, "postgres:17.11", def.Dependencies["postgres"].Image)
}

// imageRepos-prefix rewriting must apply to BOTH forms: a stand that pulls
// third-party images from its own mirror declares it once in the workspace
// config, and the dependencies section may not be the one place that ignores it.
func TestParseBundleFile_DependenciesResolveImageRepoPrefixes(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  postgres:
    image: core/postgres:17.11
  rabbitmq:
    image:
      repository: core/rabbitmq
      tag: "4.2.9-management"
`)
	assert.Equal(t, "nexus.citeck.ru/postgres:17.11", def.Dependencies["postgres"].Image,
		"string form must go through the same imageRepos rewriting")
	assert.Equal(t, "nexus.citeck.ru/rabbitmq:4.2.9-management", def.Dependencies["rabbitmq"].Image,
		"repository/tag form must go through the same imageRepos rewriting")
}

// The section is a SECTION, not an app. The main parse loop walks every
// top-level key, so without an explicit skip the `dependencies:` map is handed
// to processApp like any other entry — and an entry id that collides with the
// entry schema's own key ("image") is then read as this pseudo-app's image.
func TestParseBundleFile_DependenciesSectionIsNeverAnApp(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  image:
    repository: some/future-thing
    tag: "1.0"
  postgres:
    image: postgres:17.11
`)
	assert.NotContains(t, def.Applications, "dependencies",
		"the dependencies section must never be parsed as an application")
	assert.Equal(t, "postgres:17.11", def.Dependencies["postgres"].Image)
}

// A future launcher may know an id this one does not. Ignoring it (rather than
// failing the whole bundle) is what lets one bundle serve both.
func TestParseBundleFile_UnknownDependencyIdIsIgnoredNotAnError(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  some-future-thing:
    image: future/thing:1.0
`)
	assert.NotContains(t, def.Applications, "some-future-thing")
	assert.True(t, def.IsEmpty(), "a bundle with no Citeck apps is still empty")
}

// The compatibility contract from the other side: a bundle that names its
// images the way every bundle in the field does behaves exactly as before —
// same Applications, and no Dependencies at all.
func TestParseBundleFile_TopLevelOnlyBundleIsUnchanged(t *testing.T) {
	def := parseTestBundle(t, `
postgres:
  image:
    repository: postgres
    tag: "17.5"
gateway:
  image:
    repository: core/gateway
    tag: "1.0"
`)
	assert.Equal(t, "postgres:17.5", def.Applications["postgres"].Image)
	assert.Equal(t, "nexus.citeck.ru/gateway:1.0", def.Applications["gateway"].Image)
	assert.Empty(t, def.Dependencies, "no dependencies section ⇒ no dependencies")
}

// A bundle that carries ONLY third-party images has no Citeck apps in it, and
// IsEmpty is what raises the non-dismissible BundleErrorBanner. Merging the
// section into Applications would silence that alarm on exactly the namespace
// that needs it: seven third-party containers reporting RUNNING with none of
// the product in them.
func TestDependenciesOnlyBundleIsStillEmpty(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  postgres:
    image: postgres:17.11
  rabbitmq:
    image: rabbitmq:4.2.9-management
`)
	assert.Len(t, def.Dependencies, 2)
	assert.True(t, def.IsEmpty(),
		"a dependencies-only bundle has no Citeck apps and must still be reported as empty")
}

// A `dependencies:` entry this launcher cannot read an image out of is ignored
// rather than fatal — that is what makes the section safe to write. But silence
// is the wrong price for a typo: the author's version then simply does not
// apply, the stand keeps running the launcher's own default, and nothing
// anywhere says why. So the skip is logged, naming the id.
func TestParseBundleFile_ADependencyWithNoReadableImageIsLogged(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
dependencies:
  postgres:
    imagee: postgres:17.11
  rabbitmq:
    image: rabbitmq:4.2.9-management
`), 0o600))

	def, err := parseBundleFile(path, "test", nil, nil, logger)
	require.NoError(t, err)
	assert.NotContains(t, def.Dependencies, "postgres")
	assert.Contains(t, buf.String(), "postgres", "the skipped entry must be named in the log")
	assert.NotContains(t, buf.String(), "rabbitmq", "an entry that was read is not a finding")
}

// The one spelling that looks right and could easily not be: an UNQUOTED
// numeric tag. `tag: 17.11` is a YAML float, not a string, and reading it out
// of a generic map would hand us float64(17.11) — from which "17.10" comes back
// as "17.1". The section is therefore decoded from the YAML node itself, where
// the tag's raw text survives, and the same entry type as the workspace config
// uses, so one spelling cannot work in one file and silently fail in the other.
func TestParseBundleFile_AnUnquotedNumericTagStillNamesTheImage(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  postgres:
    image:
      repository: postgres
      tag: 17.11
  onlyoffice:
    image:
      repository: onlyoffice/documentserver
      tag: 9.4.0.1
`)
	assert.Equal(t, "postgres:17.11", def.Dependencies["postgres"].Image,
		"an unquoted tag must not silently lose its trailing digits")
	assert.Equal(t, "onlyoffice/documentserver:9.4.0.1", def.Dependencies["onlyoffice"].Image)
}

// The dependencies section is the ONE place a list means a ladder, and the
// target is its LAST rung — that is where the stand is being taken.
func TestABundleDependencyLadderKeepsEveryRungAndTargetsTheLast(t *testing.T) {
	def := parseTestBundle(t, `
dependencies:
  qdrant:
    image:
      - qdrant/qdrant:v1.15.5
      - qdrant/qdrant:v1.16.1
      - qdrant/qdrant:v1.19.1
`)
	entry := def.Dependencies["qdrant"]
	assert.Equal(t, []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	}, entry.Images)
	assert.Equal(t, "qdrant/qdrant:v1.19.1", entry.Image,
		"the target is the last rung: that is where the ladder leads")
}

// Everywhere else a list has no route to walk, so the FIRST element is taken —
// the most conservative rung, the one most likely to match what is already in
// the volume. Same principle as LegacyImage().
func TestABundleApplicationListTakesTheFirstElement(t *testing.T) {
	def := parseTestBundle(t, `
eapps:
  image:
    - harbor/ecos-eapps:1.0.0
    - harbor/ecos-eapps:2.0.0
`)
	assert.Equal(t, "harbor/ecos-eapps:1.0.0", def.Applications["eapps"].Image)
	assert.Nil(t, def.Applications["eapps"].Images,
		"an applications entry has no ladder to offer")
}

func TestAWorkspaceDependencyLadderIsRegistryResolvedAndTargetsTheLast(t *testing.T) {
	ws := parseWorkspaceConfig([]byte(`
imageRepos:
  - id: core
    url: nexus.citeck.ru
webapps: []
dependencies:
  qdrant:
    image:
      - core/qdrant:v1.15.5
      - core/qdrant:v1.19.1
`), "ws.yml", slog.Default())
	assert.Equal(t, []string{"nexus.citeck.ru/qdrant:v1.15.5", "nexus.citeck.ru/qdrant:v1.19.1"},
		ws.DependencyImageChain("qdrant"))
	assert.Equal(t, "nexus.citeck.ru/qdrant:v1.19.1", ws.DependencyImage("qdrant"))
}

func TestAWorkspaceDependencyWithALadderHoleNamesNothing(t *testing.T) {
	ws := parseWorkspaceConfig([]byte(`
imageRepos: []
webapps: []
dependencies:
  qdrant:
    image:
      - qdrant/qdrant:v1.15.5
      - {repository: qdrant/qdrant}
`), "ws.yml", slog.Default())
	assert.Empty(t, ws.DependencyImage("qdrant"))
	assert.Nil(t, ws.DependencyImageChain("qdrant"))
}
