package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wsWebappWithDeps(id string, deps []string) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID:           id,
			DefaultProps: bundle.WebappDefaultProps{DependsOn: deps},
		}},
		AdditionalApps: []bundle.AdditionalAppProps{{
			Name:  "sidecar",
			Image: "example/sidecar:1.0",
		}},
	}
}

func TestWebappDependsOn_FromWorkspaceConfig(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(basicCfg(), bun, wsWebappWithDeps("emodel", []string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.Contains(t, []string(app.DependsOn), "sidecar")
	// Захардкоженные зависимости не потеряны.
	assert.Contains(t, []string(app.DependsOn), appdef.AppZookeeper)
	assert.Contains(t, []string(app.DependsOn), appdef.AppRabbitmq)
}

func TestWebappDependsOn_NamespaceOverridesWorkspace(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{"emodel": {DependsOn: []string{}}}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(cfg, bun, wsWebappWithDeps("emodel", []string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.NotContains(t, []string(app.DependsOn), "sidecar",
		"пустой список в namespace.yml снимает зависимость из workspace")
}

func TestWebappDependsOn_SelfDependencyIsRejected(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	_, err := Generate(basicCfg(), bun, wsWebappWithDeps("emodel", []string{"emodel"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "emodel")
}

// TestWebappDependsOn_TwoAppCycleIsRejected: the self-dependency case is caught
// by its own guard in applyConfiguredDependsOn, so the recursive cycle detector
// had no test for the shape it actually exists for — a cycle that only closes
// after two hops. A namespace whose apps wait on each other never starts, and
// the launcher must say so at generation time rather than leave them in
// DEPS_WAITING forever.
func TestWebappDependsOn_TwoAppCycleIsRejected(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{
		"emodel": {DependsOn: []string{"eproc"}},
		"eproc":  {DependsOn: []string{"emodel"}},
	}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
		"eproc":  {Image: "bundle/eproc:1.0"},
	}}
	ws := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: "emodel"}, {ID: "eproc"}}}

	_, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.Error(t, err, "a two-app cycle must fail generation, not park both apps in DEPS_WAITING")
	assert.Contains(t, err.Error(), "dependsOn cycle")
}

// TestWebappDependsOn_NamespaceEntryWithoutDependsOnKeepsWorkspaceDeps: the
// namespace override is nil-vs-empty sensitive. An app mentioned in
// namespace.yml for some OTHER reason (heapSize, image…) has a nil DependsOn,
// which must leave the workspace list alone — only an explicitly written list
// (including an empty one) replaces it.
func TestWebappDependsOn_NamespaceEntryWithoutDependsOnKeepsWorkspaceDeps(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{"emodel": {HeapSize: "512m"}}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(cfg, bun, wsWebappWithDeps("emodel", []string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.Contains(t, []string(app.DependsOn), "sidecar",
		"a namespace entry that says nothing about dependsOn must not silently drop the workspace list")
}
