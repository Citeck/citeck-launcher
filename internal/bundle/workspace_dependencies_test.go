package bundle

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseTestWorkspace parses a workspace-v1.yml body the way every load path
// does. parseWorkspaceConfig returns nil on a YAML error and the resolver then
// falls through to an EMPTY workspace config, so "did it parse" is a real
// assertion here and not a formality: a `dependencies:` section that fails to
// decode would take the imageRepos, the webapps and the bundleRepos of every
// namespace in the workspace down with it.
func parseTestWorkspace(t *testing.T, yml string) *WorkspaceConfig {
	t.Helper()
	cfg := parseWorkspaceConfig([]byte(yml), "workspace-v1.yml", nil)
	require.NotNil(t, cfg, "the workspace config must still parse")
	return cfg
}

// The owner's own spelling of the new section: a plain image string.
func TestWorkspaceConfig_DependenciesAcceptAPlainImageString(t *testing.T) {
	cfg := parseTestWorkspace(t, `
dependencies:
  postgres:
    image: postgres:17.11
  rabbitmq:
    image: rabbitmq:4.2.9-management
`)
	assert.Equal(t, "postgres:17.11", cfg.DependencyImage("postgres"))
	assert.Equal(t, "rabbitmq:4.2.9-management", cfg.DependencyImage("rabbitmq"))
}

// The {repository, tag} map form is how every bundle entry and every typed
// workspace block names an image, so a config author's habit has to work here.
func TestWorkspaceConfig_DependenciesAcceptTheRepositoryTagForm(t *testing.T) {
	cfg := parseTestWorkspace(t, `
dependencies:
  postgres:
    image:
      repository: postgres
      tag: "17.11"
`)
	assert.Equal(t, "postgres:17.11", cfg.DependencyImage("postgres"))
}

// imageRepos-prefix rewriting must apply to BOTH forms: a stand that pulls
// third-party images from its own mirror declares that once, and the new
// section may not be the one place that ignores it.
func TestWorkspaceConfig_DependenciesResolveImageRepoPrefixes(t *testing.T) {
	cfg := parseTestWorkspace(t, `
imageRepos:
  - id: core
    url: nexus.citeck.ru
dependencies:
  postgres:
    image: core/postgres:17.11
  rabbitmq:
    image:
      repository: core/rabbitmq
      tag: "4.2.9-management"
`)
	assert.Equal(t, "nexus.citeck.ru/postgres:17.11", cfg.DependencyImage("postgres"),
		"string form must go through the same imageRepos rewriting")
	assert.Equal(t, "nexus.citeck.ru/rabbitmq:4.2.9-management", cfg.DependencyImage("rabbitmq"),
		"repository/tag form must go through the same imageRepos rewriting")
}

// A future launcher may know an id this one does not, and an entry shape this
// one does not understand must not cost the workspace its whole config.
func TestWorkspaceConfig_MalformedDependencyEntriesAreIgnoredNotFatal(t *testing.T) {
	cfg := parseTestWorkspace(t, `
dependencies:
  some-future-thing:
    image: future/thing:1.0
  nonsense: "not a mapping"
  imageless:
    memoryLimit: 2g
  postgres:
    image: postgres:17.11
`)
	assert.Equal(t, "future/thing:1.0", cfg.DependencyImage("some-future-thing"),
		"an id this launcher does not know is kept, never asked for, and never an error")
	assert.Empty(t, cfg.DependencyImage("nonsense"))
	assert.Empty(t, cfg.DependencyImage("imageless"))
	assert.Equal(t, "postgres:17.11", cfg.DependencyImage("postgres"),
		"an adjacent entry this launcher cannot read must not hide a valid one")
}

// The compatibility contract from the other side: every workspace config in the
// field has no `dependencies:` section, and must behave exactly as it does today.
func TestWorkspaceConfig_WithoutTheSectionIsUnchanged(t *testing.T) {
	cfg := parseTestWorkspace(t, `
imageRepos:
  - id: core
    url: nexus.citeck.ru
postgres:
  image: postgres:17.9
webapps:
  - id: gateway
`)
	assert.Empty(t, cfg.Dependencies, "no dependencies section ⇒ no dependencies")
	assert.Empty(t, cfg.DependencyImage("postgres"),
		"the typed block is NOT the section; reading it here would invert the chain")
	assert.Equal(t, "postgres:17.9", string(cfg.Postgres.Image), "the typed block still parses as before")

	var nilCfg *WorkspaceConfig
	assert.Empty(t, nilCfg.DependencyImage("postgres"), "nil receiver answers nothing")
}

// Same rule as the bundle side: an entry whose image cannot be read costs only
// itself, but it must not cost it silently — a typo that leaves the stand on
// the launcher's own default with no line anywhere is the worst of both.
func TestWorkspaceConfig_ADependencyWithNoReadableImageIsLogged(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := parseWorkspaceConfig([]byte(`
dependencies:
  postgres:
    imagee: postgres:17.11
  rabbitmq:
    image: rabbitmq:4.2.9-management
`), "workspace-v1.yml", logger)
	require.NotNil(t, cfg, "one unreadable entry must not cost the whole config")

	assert.Empty(t, cfg.DependencyImage("postgres"))
	assert.Contains(t, buf.String(), "postgres", "the skipped entry must be named in the log")
	assert.NotContains(t, buf.String(), "rabbitmq", "an entry that was read is not a finding")
}

// The workspace side decodes from the YAML node, so an unquoted numeric tag
// keeps its raw text. Pinned because the bundle side had to be brought to this
// same behavior: one spelling must not work in one file and fail in the other.
func TestWorkspaceConfig_AnUnquotedNumericTagStillNamesTheImage(t *testing.T) {
	cfg := parseTestWorkspace(t, `
dependencies:
  postgres:
    image:
      repository: postgres
      tag: 17.11
`)
	assert.Equal(t, "postgres:17.11", cfg.DependencyImage("postgres"))
}
