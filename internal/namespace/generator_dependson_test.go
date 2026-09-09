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
