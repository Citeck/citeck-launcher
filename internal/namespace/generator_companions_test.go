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

func TestWorkspaceCompanionsOverrideDefaultPropsAndRescueMissingSecret(t *testing.T) {
	makeWorkspace := func(gatewayDependsOn ...string) *bundle.WorkspaceConfig {
		return &bundle.WorkspaceConfig{
			Webapps: []bundle.WebappConfig{
				{
					ID: appdef.AppAi,
					DefaultProps: bundle.WebappDefaultProps{
						Environments: map[string]string{
							"LLM_API_KEY":  "${secret:direct-provider-key}",
							"LLM_BASE_URL": "https://direct.example.com",
						},
					},
					Companions: map[string]bundle.WebappCompanionProps{
						companionGateway: {
							Environments: map[string]string{
								"LLM_API_KEY":  "sk-${secret:gateway-key}",
								"LLM_BASE_URL": "http://" + companionGateway + ":4000",
							},
							DependsOn: []string{companionGateway},
						},
					},
				},
				{
					ID: appdef.AppRag,
					DefaultProps: bundle.WebappDefaultProps{
						DependsOn: []string{appdef.AppAi},
					},
				},
			},
			AdditionalApps: []bundle.AdditionalAppProps{{
				Name:         companionGateway,
				Environments: map[string]string{"MASTER_KEY": "sk-${secret:gateway-key}"},
				DependsOn:    gatewayDependsOn,
			}},
		}
	}

	t.Run("surviving companion rescues webapp and its consumers", func(t *testing.T) {
		resp := generateWithCompanions(t, basicCfg(), companionBundle(true), makeWorkspace(),
			map[string]string{"gateway-key": "gw-secret"})

		require.NotNil(t, findGeneratedApp(resp, companionGateway))
		ai := findGeneratedApp(resp, appdef.AppAi)
		require.NotNil(t, ai, "surviving companion overrides missing direct-provider-key and rescues ai")
		gotKey, _ := ai.Environments.Get("LLM_API_KEY")
		gotURL, _ := ai.Environments.Get("LLM_BASE_URL")
		assert.Equal(t, "sk-gw-secret", gotKey)
		assert.Equal(t, "http://llm-gateway:4000", gotURL)
		assert.Contains(t, ai.DependsOn, companionGateway)
		assert.NotNil(t, findGeneratedApp(resp, appdef.AppRag), "consumer of rescued webapp stays too")
	})

	t.Run("without companion, unoverridden missing secret still excludes webapp and consumers", func(t *testing.T) {
		resp := generateWithCompanions(t, basicCfg(), companionBundle(false), makeWorkspace(),
			map[string]string{"gateway-key": "gw-secret"})

		assert.Nil(t, findGeneratedApp(resp, companionGateway))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppAi))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))
	})

	t.Run("companion excluded for missing own secret does not rescue webapp", func(t *testing.T) {
		resp := generateWithCompanions(t, basicCfg(), companionBundle(true), makeWorkspace(), nil)

		assert.Nil(t, findGeneratedApp(resp, companionGateway))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppAi))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))
	})

	t.Run("companion pruned for missing dependency does not rescue webapp", func(t *testing.T) {
		resp := generateWithCompanions(t, basicCfg(), companionBundle(true), makeWorkspace("missing-db"),
			map[string]string{"gateway-key": "gw-secret"})

		assert.Nil(t, findGeneratedApp(resp, companionGateway))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppAi))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))
	})

	t.Run("companion depending on secret-excluded helper does not rescue webapp", func(t *testing.T) {
		ws := makeWorkspace("gateway-helper")
		ws.AdditionalApps = append(ws.AdditionalApps, bundle.AdditionalAppProps{
			Name:         "gateway-helper",
			Image:        "example/helper:1.0.0",
			Environments: map[string]string{"HELPER_TOKEN": "${secret:missing-helper-secret}"},
		})
		resp := generateWithCompanions(t, basicCfg(), companionBundle(true), ws,
			map[string]string{"gateway-key": "gw-secret"})

		assert.Nil(t, findGeneratedApp(resp, "gateway-helper"))
		assert.Nil(t, findGeneratedApp(resp, companionGateway))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppAi))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))
	})

	t.Run("second unoverridden missing secret still excludes webapp", func(t *testing.T) {
		ws := makeWorkspace()
		ws.Webapps[0].DefaultProps.Environments["OTHER_SECRET"] = "${secret:unrescued}"
		resp := generateWithCompanions(t, basicCfg(), companionBundle(true), ws,
			map[string]string{"gateway-key": "gw-secret"})

		require.NotNil(t, findGeneratedApp(resp, companionGateway))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppAi))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))
	})
}

func TestWorkspaceCompanionsNamespaceEnvSkipsMissingCompanionSecret(t *testing.T) {
	ws := companionWorkspace()
	block := ws.Webapps[0].Companions[companionGateway]
	block.Environments["LLM_EXTRA"] = "${secret:missing-override-secret}"
	ws.Webapps[0].Companions[companionGateway] = block

	cfg := basicCfg()
	cfg.Webapps = map[string]WebappProps{
		appdef.AppAi: {Environments: map[string]string{"LLM_EXTRA": "operator-value"}},
	}

	resp := generateWithCompanions(t, cfg, companionBundle(true), ws,
		map[string]string{"gateway-key": "abc"})

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	gotExtra, _ := ai.Environments.Get("LLM_EXTRA")
	assert.Equal(t, "operator-value", gotExtra, "namespace env wins and skips secret check for that key")
	gotURL, ok := ai.Environments.Get("LLM_BASE_URL")
	assert.True(t, ok, "remaining companion keys must still be applied")
	assert.Equal(t, "http://llm-gateway:4000", gotURL)
	assert.Contains(t, ai.DependsOn, companionGateway)
}

func TestWorkspaceCompanionsOnlyTargetGeneratedWebappsAndFirstDuplicateEntry(t *testing.T) {
	ws := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			// Not a generated webapp (postgres is infra): companions must never touch it.
			{
				ID: appdef.AppPostgres,
				Companions: map[string]bundle.WebappCompanionProps{
					companionGateway: {
						Environments: map[string]string{"SHOULD_NOT_BE_SET": "1"},
						DependsOn:    []string{companionGateway},
					},
				},
			},
			// First entry for ai wins; duplicate second entry is ignored.
			{
				ID: appdef.AppAi,
				Companions: map[string]bundle.WebappCompanionProps{
					companionGateway: {
						Environments: map[string]string{"FROM_ENTRY": "first"},
						DependsOn:    []string{companionGateway},
					},
				},
			},
			{
				ID: appdef.AppAi,
				Companions: map[string]bundle.WebappCompanionProps{
					companionGateway: {
						Environments: map[string]string{"FROM_ENTRY": "second", "SECOND_ONLY": "1"},
					},
				},
			},
		},
		AdditionalApps: []bundle.AdditionalAppProps{{
			Name:  companionGateway,
			Image: "example/gateway:1.0.0",
		}},
	}

	resp := generateWithCompanions(t, basicCfg(), companionBundle(true), ws, nil)

	pg := findGeneratedApp(resp, appdef.AppPostgres)
	require.NotNil(t, pg)
	_, hasEnv := pg.Environments.Get("SHOULD_NOT_BE_SET")
	assert.False(t, hasEnv, "companions must not apply to infra/non-webapp containers")
	assert.NotContains(t, pg.DependsOn, companionGateway)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	got, _ := ai.Environments.Get("FROM_ENTRY")
	assert.Equal(t, "first", got, "first duplicate ws.Webapps entry must win")
	_, hasSecond := ai.Environments.Get("SECOND_ONLY")
	assert.False(t, hasSecond)
}

func TestWorkspaceCompanionsSkipCyclicSelfAndMissingDependsOn(t *testing.T) {
	// Transitive cycle: llm-gateway -> gateway-helper -> ai, while ai's
	// companion wants dependsOn: [llm-gateway, ai, does-not-exist].
	ws := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{
				ID: appdef.AppAi,
				Companions: map[string]bundle.WebappCompanionProps{
					appdef.AppAi: {
						Environments: map[string]string{"SELF_COMPANION": "1"},
					},
					companionGateway: {
						Environments: map[string]string{"LLM_BASE_URL": "http://" + companionGateway + ":4000"},
						DependsOn:    []string{companionGateway, appdef.AppAi, "does-not-exist"},
					},
				},
			},
		},
		AdditionalApps: []bundle.AdditionalAppProps{
			{
				Name:      companionGateway,
				Image:     "example/gateway:1.0.0",
				DependsOn: []string{"gateway-helper"},
			},
			{
				Name:      "gateway-helper",
				Image:     "example/helper:1.0.0",
				DependsOn: []string{appdef.AppAi},
			},
		},
	}

	resp := generateWithCompanions(t, basicCfg(), companionBundle(true), ws, nil)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, hasSelf := ai.Environments.Get("SELF_COMPANION")
	assert.False(t, hasSelf, "companion == ws.ID must be ignored")
	gotURL, ok := ai.Environments.Get("LLM_BASE_URL")
	assert.True(t, ok, "non-cyclic env still applies when cyclic dependsOn edge is skipped")
	assert.Equal(t, "http://llm-gateway:4000", gotURL)
	assert.NotContains(t, ai.DependsOn, companionGateway, "transitive cyclic edge ai -> llm-gateway must be skipped")
	assert.NotContains(t, ai.DependsOn, appdef.AppAi, "self-dependency dep == appName must be skipped")
	assert.NotContains(t, ai.DependsOn, "does-not-exist", "missing dep must be skipped without pruning")
}

func TestWorkspaceCompanionsMultipleCompanionsApplyDeterministically(t *testing.T) {
	ws := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{
				ID: appdef.AppAi,
				DefaultProps: bundle.WebappDefaultProps{
					Environments: map[string]string{
						"SHARED":  "from-default",
						"DEFAULT": "kept",
					},
				},
				Companions: map[string]bundle.WebappCompanionProps{
					"beta-comp": {
						Environments: map[string]string{"SHARED": "from-beta", "BETA_ONLY": "2"},
						DependsOn:    []string{"beta-comp"},
					},
					"alpha-comp": {
						Environments: map[string]string{"SHARED": "from-alpha", "ALPHA_ONLY": "1"},
						DependsOn:    []string{"alpha-comp"},
					},
				},
			},
		},
		AdditionalApps: []bundle.AdditionalAppProps{
			{Name: "alpha-comp", Image: "example/alpha:1.0.0"},
			{Name: "beta-comp", Image: "example/beta:1.0.0"},
		},
	}

	resp := generateWithCompanions(t, basicCfg(), companionBundle(false), ws, nil)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	for key, want := range map[string]string{
		"DEFAULT":    "kept",
		"ALPHA_ONLY": "1",
		"BETA_ONLY":  "2",
		"SHARED":     "from-beta", // sorted companion order: alpha-comp then beta-comp
	} {
		got, ok := ai.Environments.Get(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
	}
	assert.Contains(t, ai.DependsOn, "alpha-comp")
	assert.Contains(t, ai.DependsOn, "beta-comp")
}

// Variant A contract: companions bind by presence in ctx.Applications, not by
// DetachedApps, and never mark the companion as a gating app.
func TestWorkspaceCompanionsStayWiredWhenCompanionIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), companionBundle(true), companionWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{
			DetachedApps:     map[string]bool{companionGateway: true},
			NamespaceSecrets: map[string]string{"gateway-key": "abc"},
		})
	require.NoError(t, err)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	gotURL, ok := ai.Environments.Get("LLM_BASE_URL")
	assert.True(t, ok, "detached companion stays wired under Variant A")
	assert.Equal(t, "http://llm-gateway:4000", gotURL)
	assert.Contains(t, ai.DependsOn, companionGateway)
	assert.False(t, resp.GatingApps[companionGateway], "companions must not call MarkGatingApp")
	assert.True(t, resp.DependsOnDetachedApps[companionGateway], "dependsOnDetachedApps tracks the detached dependency")
}
