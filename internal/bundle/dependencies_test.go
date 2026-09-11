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
