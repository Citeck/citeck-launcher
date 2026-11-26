package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const companionGateway = "llm-gateway"

func companionBundle(withGateway bool) *bundle.Def {
	apps := map[string]bundle.AppDef{
		appdef.AppAi:  {Image: "citeck/ai:1.0.0"},
		appdef.AppRag: {Image: "citeck/rag:1.0.0"},
	}
	if withGateway {
		apps[companionGateway] = bundle.AppDef{Image: "example/gateway:1.0.0"}
	}
	return &bundle.Def{Applications: apps}
}

// companionWorkspace declares the gateway as a raw additional app (its image
// comes from the bundle) and the settings ai receives while it is present.
func companionWorkspace(gatewayDependsOn ...string) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: appdef.AppAi, Companions: map[string]bundle.WebappCompanionProps{
				companionGateway: {
					Environments: map[string]string{
						"LLM_BASE_URL": "http://" + companionGateway + ":4000",
						"LLM_API_KEY":  "sk-${secret:gateway-key}",
						"LLM_MODEL":    "citeck-chat",
						"LLM_ZK":       "${ZK_HOST}",
					},
					DependsOn: []string{companionGateway},
				},
			}},
			{ID: appdef.AppRag},
		},
		AdditionalApps: []bundle.AdditionalAppProps{{
			Name:         companionGateway,
			Environments: map[string]string{"MASTER_KEY": "sk-${secret:gateway-key}"},
			DependsOn:    gatewayDependsOn,
		}},
	}
}

func generateWithCompanions(t *testing.T, cfg *Config, bun *bundle.Def, ws *bundle.WorkspaceConfig,
	secrets map[string]string,
) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{NamespaceSecrets: secrets})
	require.NoError(t, err)
	return resp
}

func TestWorkspaceCompanionsWireWebappWhileCompanionIsPresent(t *testing.T) {
	resp := generateWithCompanions(t, basicCfg(), companionBundle(true), companionWorkspace(),
		map[string]string{"gateway-key": "abc"})

	require.NotNil(t, findGeneratedApp(resp, companionGateway), "the companion itself is generated")
	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)

	for key, want := range map[string]string{
		"LLM_BASE_URL": "http://llm-gateway:4000",
		"LLM_API_KEY":  "sk-abc",
		"LLM_MODEL":    "citeck-chat",
		"LLM_ZK":       ZKHost,
	} {
		got, ok := ai.Environments.Get(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
	}
	assert.Contains(t, ai.DependsOn, companionGateway)

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	_, ok := rag.Environments.Get("LLM_BASE_URL")
	assert.False(t, ok, "only the webapp that declares the companion is wired")
}

func TestWorkspaceCompanionsLeaveWebappAloneWithoutCompanion(t *testing.T) {
	cases := map[string]struct {
		bun     *bundle.Def
		ws      *bundle.WorkspaceConfig
		secrets map[string]string
	}{
		// The experimental feature is off: the bundle names no image.
		"bundle names no image": {companionBundle(false), companionWorkspace(), map[string]string{"gateway-key": "abc"}},
		// The companion's own secret has no value: excludeAppsMissingSecrets
		// removes it before the companions are wired.
		"companion secret missing": {companionBundle(true), companionWorkspace(), nil},
		// The companion's own dependency is absent: the prune removes it. Had
		// the dependsOn been added before the prune, ai would be gone as well.
		"companion pruned": {companionBundle(true), companionWorkspace("llm-gateway-db"), map[string]string{"gateway-key": "abc"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resp := generateWithCompanions(t, basicCfg(), tc.bun, tc.ws, tc.secrets)
			assert.Nil(t, findGeneratedApp(resp, companionGateway))
			ai := findGeneratedApp(resp, appdef.AppAi)
			require.NotNil(t, ai, "ai stays in the namespace")
			_, ok := ai.Environments.Get("LLM_BASE_URL")
			assert.False(t, ok)
			assert.NotContains(t, ai.DependsOn, companionGateway)
		})
	}
}

func TestWorkspaceCompanionsNamespaceEnvWins(t *testing.T) {
	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{
		appdef.AppAi: {Environments: map[string]string{"LLM_MODEL": "my-model"}},
	}
	resp := generateWithCompanions(t, cfg, companionBundle(true), companionWorkspace(),
		map[string]string{"gateway-key": "abc"})

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	got, _ := ai.Environments.Get("LLM_MODEL")
	assert.Equal(t, "my-model", got, "namespace.yml is the operator's file")
	got, _ = ai.Environments.Get("LLM_BASE_URL")
	assert.Equal(t, "http://llm-gateway:4000", got, "the other keys still apply")
}

// A block that needs a secret the companion itself does not reference is
// skipped whole; ai is neither half-wired nor excluded.
func TestWorkspaceCompanionsBlockWithMissingSecretIsSkippedWhole(t *testing.T) {
	ws := companionWorkspace()
	block := ws.Webapps[0].Companions[companionGateway]
	block.Environments["LLM_EXTRA"] = "${secret:not-declared}"
	ws.Webapps[0].Companions[companionGateway] = block

	resp := generateWithCompanions(t, basicCfg(), companionBundle(true), ws,
		map[string]string{"gateway-key": "abc"})

	require.NotNil(t, findGeneratedApp(resp, companionGateway))
	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai, "a missing key must not cost the namespace its ai")
	_, ok := ai.Environments.Get("LLM_BASE_URL")
	assert.False(t, ok)
	assert.NotContains(t, ai.DependsOn, companionGateway)
}
