package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsWebappWithDeps builds a workspace config whose emodel entry declares the
// given dependsOn list, plus the "sidecar" additional app those lists point at.
func wsWebappWithDeps(deps []string) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID:           "emodel",
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

	resp, err := Generate(basicCfg(), bun, wsWebappWithDeps([]string{"sidecar"}),
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

	resp, err := Generate(cfg, bun, wsWebappWithDeps([]string{"sidecar"}),
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

	_, err := Generate(basicCfg(), bun, wsWebappWithDeps([]string{"emodel"}),
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

	resp, err := Generate(cfg, bun, wsWebappWithDeps([]string{"sidecar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "emodel")
	require.NotNil(t, app)
	assert.Contains(t, []string(app.DependsOn), "sidecar",
		"a namespace entry that says nothing about dependsOn must not silently drop the workspace list")
}

// A namespace.yml dependsOn naming an app that does not exist is a typo, and it
// used to be answered by pruneAppsWithMissingDeps: the webapp that named it was
// deleted from the namespace — together with anything depending on it — leaving
// only an slog.Error. So `webapps.emodel.dependsOn: [sidcar]` made emodel
// vanish. A self-dependency was already a reported error; an unknown target now
// is too.
func TestWebappDependsOn_UnknownNamespaceTargetIsReportedNotPruned(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{"emodel": {DependsOn: []string{"sidcar"}}}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	_, err := Generate(cfg, bun, nil, SystemSecrets{JWT: "j", OIDC: "o"})

	require.Error(t, err, "опечатка в dependsOn должна быть названа, а не стоить аппу места в неймспейсе")
	assert.Contains(t, err.Error(), "sidcar")
	assert.Contains(t, err.Error(), "emodel")
}

// The WORKSPACE layer keeps the prune. That file lives in a git repo the local
// operator usually cannot edit and is shared by every namespace on the
// workspace, so a target that is legitimately absent here (an app turned off in
// namespace.yml, keycloak under BASIC auth) must not take the whole namespace
// down — it costs the naming webapp its place, as it always did.
func TestWebappDependsOn_UnknownWorkspaceTargetStillPrunes(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	resp, err := Generate(basicCfg(), bun, wsWebappWithDeps([]string{"sidcar"}),
		SystemSecrets{JWT: "j", OIDC: "o"})

	require.NoError(t, err, "неймспейс не должен падать целиком из-за чужого файла")
	assert.Nil(t, findGeneratedApp(resp, "emodel"),
		"апп с недостижимой зависимостью по-прежнему вырезается")
}

// The rule covers only what an OPERATOR wrote. A generator naming an app this
// mode does not produce (keycloak under BASIC auth is the standing example) is
// normal, and pruning stays the right answer for it — turning that into a hard
// error would fail generation on every namespace that does not run keycloak.
func TestWebappDependsOn_GeneratorEmittedTargetsAreStillPruned(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"emodel": {Image: "bundle/emodel:1.0"},
	}}

	// basicCfg() is BASIC auth, so no keycloak is generated, yet webapps carry
	// generator-emitted wiring for it.
	resp, err := Generate(basicCfg(), bun, nil, SystemSecrets{JWT: "j", OIDC: "o"})

	require.NoError(t, err)
	assert.NotNil(t, findGeneratedApp(resp, "emodel"))
}
