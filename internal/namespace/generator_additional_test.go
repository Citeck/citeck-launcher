package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// basicCfg is a minimal BASIC-auth namespace config for the additionalApps tests
// (additionalApps now live in the workspace config, not the namespace config).
func basicCfg() *Config {
	return &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
}

// wsWithApps builds a workspace config carrying the given additionalApps. Optional
// imageRepos let a test exercise the "<repoId>/path:tag" resolution.
func wsWithApps(apps []bundle.AdditionalAppProps, imageRepos ...bundle.ImageRepo) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{AdditionalApps: apps, ImageRepos: imageRepos}
}

// ediSimAdditionalApp is the config-only definition of the EDI simulator — the
// motivating example: a custom Go service added to a namespace by configuration
// alone, with no dedicated launcher generator.
func ediSimAdditionalApp() bundle.AdditionalAppProps {
	return bundle.AdditionalAppProps{
		Name:           "edi-sim",
		Image:          "registry.citeck.ru/community/citeck-edi-sim:0.1.0",
		NetworkAliases: []string{"EcosEdiSimApp"},
		DependsOn:      []string{appdef.AppZookeeper},
		Environments: map[string]string{
			"ZOOKEEPER_HOSTS":   "${ZK_HOST}:${ZK_PORT}",
			"DISCOVERY_APPNAME": "edi-sim",
		},
		Ports:         []string{"8080"},
		LivenessProbe: &appdef.AppProbeDef{HTTP: &appdef.HTTPProbeDef{Path: "/health", Port: 8080}},
	}
}

func TestGenerateAdditionalApps_EdiSim(t *testing.T) {
	config.ResetDesktopMode() // server mode: only proxy publishes ports
	ws := wsWithApps([]bundle.AdditionalAppProps{ediSimAdditionalApp()})

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "edi-sim")
	require.NotNil(t, app, "edi-sim additional app must be generated")
	assert.Equal(t, "registry.citeck.ru/community/citeck-edi-sim:0.1.0", app.Image)
	assert.Equal(t, appdef.KindThirdParty, app.Kind, "default kind is THIRD_PARTY")
	assert.Contains(t, app.NetworkAliases, "EcosEdiSimApp")
	assert.Contains(t, []string(app.DependsOn), appdef.AppZookeeper)

	// Template vars are resolved (${ZK_HOST}/${ZK_PORT} → the real ZK host:port).
	zk, ok := app.Environments.Get("ZOOKEEPER_HOSTS")
	require.True(t, ok)
	assert.NotContains(t, zk, "${", "template variables must be resolved")
	assert.Contains(t, zk, ZKHost)

	require.NotNil(t, app.LivenessProbe)
	require.NotNil(t, app.LivenessProbe.HTTP)
	assert.Equal(t, "/health", app.LivenessProbe.HTTP.Path)

	// Server mode: the additional app is internal to the Docker network (no published ports).
	assert.Empty(t, app.Ports, "server mode must strip ports for non-proxy apps")
}

func TestGenerateAdditionalApps_Disabled(t *testing.T) {
	config.ResetDesktopMode()
	def := ediSimAdditionalApp()
	disabled := false
	def.Enabled = &disabled
	ws := wsWithApps([]bundle.AdditionalAppProps{def})
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, "edi-sim"), "disabled additional app must not be generated")
}

// TestGenerateAdditionalApps_FullSurface proves additionalApps can express every
// container-level ApplicationDef knob — not just the EDI-sim subset — so that
// genuinely any app can be added by configuration alone: cmd, stopTimeout,
// init actions and init containers, all with ${VAR} resolution.
func TestGenerateAdditionalApps_FullSurface(t *testing.T) {
	config.ResetDesktopMode()
	var icEnv appdef.OrderedMap
	icEnv.Set("PGHOST", "${PG_HOST}")
	def := bundle.AdditionalAppProps{
		Name:        "custom-svc",
		Image:       "example.com/custom:1.2.3",
		Kind:        "CITECK_ADDITIONAL",
		Cmd:         []string{"serve", "--zk=${ZK_HOST}:${ZK_PORT}"},
		ShmSize:     "256m",
		StopTimeout: 45,
		Resources:   &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}},
		InitActions: []appdef.AppInitAction{
			{Exec: []string{"sh", "-c", "echo ${PG_HOST}"}},
		},
		InitContainers: []appdef.InitContainerDef{
			{
				Image:        "busybox:1.36",
				Cmd:          []string{"sh", "-c", "until nc -z ${PG_HOST} ${PG_PORT}; do sleep 1; done"},
				Environments: icEnv,
			},
		},
	}
	ws := wsWithApps([]bundle.AdditionalAppProps{def})
	require.NoError(t, bundle.ValidateAdditionalApps(ws.AdditionalApps))

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "custom-svc")
	require.NotNil(t, app)
	assert.Equal(t, appdef.KindCiteckAdditional, app.Kind)
	assert.Equal(t, "256m", app.ShmSize)
	assert.Equal(t, 45, app.StopTimeout, "stopTimeout must propagate")
	require.NotNil(t, app.Resources)
	assert.Equal(t, "512m", app.Resources.Limits.Memory)

	// cmd template vars resolved, no literal ${...} survives.
	require.Len(t, app.Cmd, 2)
	assert.NotContains(t, app.Cmd[1], "${")
	assert.Contains(t, app.Cmd[1], ZKHost)

	// init action exec resolved.
	require.Len(t, app.InitActions, 1)
	require.Len(t, app.InitActions[0].Exec, 3)
	assert.Equal(t, PGHost, app.InitActions[0].Exec[2][len("echo "):])

	// init container resolved (cmd + env), image/passthrough intact.
	require.Len(t, app.InitContainers, 1)
	ic := app.InitContainers[0]
	assert.Equal(t, "busybox:1.36", ic.Image)
	assert.NotContains(t, ic.Cmd[2], "${", "init-container cmd vars resolved")
	v, ok := ic.Environments.Get("PGHOST")
	require.True(t, ok)
	assert.Equal(t, PGHost, v, "init-container env vars resolved")
}

// TestGenerateAdditionalApps_ImageRepoPrefix proves a bundle-style "core/foo:tag"
// image (and init-container image) is rewritten through the workspace imageRepos,
// exactly like the launcher's built-in apps.
func TestGenerateAdditionalApps_ImageRepoPrefix(t *testing.T) {
	config.ResetDesktopMode()
	def := bundle.AdditionalAppProps{
		Name:  "custom-svc",
		Image: "core/citeck-edi-sim:0.1.0-SNAPSHOT",
		InitContainers: []appdef.InitContainerDef{
			{Image: "enterprise/wait-for:1.0"},
		},
	}
	ws := wsWithApps([]bundle.AdditionalAppProps{def},
		bundle.ImageRepo{ID: "core", URL: "nexus.citeck.ru"},
		bundle.ImageRepo{ID: "enterprise", URL: "enterprise-registry.citeck.ru"},
	)

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "custom-svc")
	require.NotNil(t, app)
	assert.Equal(t, "nexus.citeck.ru/citeck-edi-sim:0.1.0-SNAPSHOT", app.Image, "app image prefix resolved via imageRepos")
	require.Len(t, app.InitContainers, 1)
	assert.Equal(t, "enterprise-registry.citeck.ru/wait-for:1.0", app.InitContainers[0].Image, "init-container image prefix resolved via imageRepos")
}

// TestGenerateAdditionalApps_DoesNotOverwriteBundleWebapp guards the regression
// where an additionalApps entry whose name matches a bundle-loaded webapp ID (not
// in the static reservedAppNames list) silently overwrote that real webapp's image
// via GetOrCreateApp returning the same builder. The built-in app must win and the
// colliding additional entry must be skipped.
func TestGenerateAdditionalApps_DoesNotOverwriteBundleWebapp(t *testing.T) {
	config.ResetDesktopMode()
	ws := wsWithApps([]bundle.AdditionalAppProps{{Name: "edi", Image: "evil/image:1.0"}})
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"edi": {Image: "registry.citeck.ru/real-edi:1.0.0"},
	}}

	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "edi")
	require.NotNil(t, app, "the real edi webapp must still be generated")
	assert.Equal(t, "registry.citeck.ru/real-edi:1.0.0", app.Image,
		"a colliding additionalApps entry must not overwrite the real webapp image")
}

// TestPruneAdditionalApps_MissingDep: an additionalApp depending on an app that
// was never generated (typo / disabled-by-mode / undefined) is excluded from the
// final set rather than silently started without its dependency.
func TestPruneAdditionalApps_MissingDep(t *testing.T) {
	config.ResetDesktopMode()
	ws := wsWithApps([]bundle.AdditionalAppProps{
		{Name: "needs-ghost", Image: "x:1", DependsOn: []string{"does-not-exist"}},
	})
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, "needs-ghost"),
		"additionalApp with a missing dependency must be excluded")
}

// TestPruneAdditionalApps_DepOnPresentApp: an additionalApp depending on a present
// built-in (zookeeper) survives — only genuinely-missing deps cause exclusion.
func TestPruneAdditionalApps_DepOnPresentApp(t *testing.T) {
	config.ResetDesktopMode()
	ws := wsWithApps([]bundle.AdditionalAppProps{
		{Name: "needs-zk", Image: "x:1", DependsOn: []string{appdef.AppZookeeper}},
	})
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	require.NotNil(t, findGeneratedApp(resp, "needs-zk"),
		"additionalApp depending on a present built-in must survive")
}

// TestPruneAdditionalApps_TransitiveChain: A→B→C where C is undefined. B (depends
// on absent C) is pruned, which makes A's dep B absent, so A is pruned too. An
// independent app D survives.
func TestPruneAdditionalApps_TransitiveChain(t *testing.T) {
	config.ResetDesktopMode()
	ws := wsWithApps([]bundle.AdditionalAppProps{
		{Name: "A", Image: "x:1", DependsOn: []string{"B"}},
		{Name: "B", Image: "x:1", DependsOn: []string{"C"}}, // C is never defined
		{Name: "D", Image: "x:1"},
	})
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, "B"), "B depends on absent C → pruned")
	assert.Nil(t, findGeneratedApp(resp, "A"), "A depends on pruned B → pruned transitively")
	require.NotNil(t, findGeneratedApp(resp, "D"), "independent D must survive")
}

// TestGenericWebappDoesNotDependOnKeycloak locks the default rule (matching Kotlin
// 1.x): an ordinary webapp's dependsOn is [ZK, RMQ] in BOTH auth modes — it never
// declares a dependsOn keycloak. So it carries no phantom dep on an app absent in
// BASIC mode, and its deployment hash is stable across KEYCLOAK ↔ BASIC. (emodel is
// the deliberate exception — see TestKeycloakIntegratedAppsDependOnKeycloak.)
func TestGenericWebappDoesNotDependOnKeycloak(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"gateway": {Image: "registry.citeck.ru/gateway:1.0.0"},
		"uiserv":  {Image: "registry.citeck.ru/uiserv:1.0.0"},
	}}
	gen := func(auth AuthenticationType) *appdef.ApplicationDef {
		cfg := &Config{
			Authentication: AuthenticationProps{Type: auth, Users: []string{"admin"}},
			Proxy:          ProxyProps{Port: 80},
		}
		resp, err := Generate(cfg, bun, nil, SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		app := findGeneratedApp(resp, "uiserv")
		require.NotNil(t, app, "uiserv webapp must be generated")
		return app
	}
	basic := gen(AuthBasic)
	kc := gen(AuthKeycloak)
	assert.NotContains(t, []string(basic.DependsOn), appdef.AppKeycloak, "generic webapp must not depend on keycloak")
	assert.NotContains(t, []string(kc.DependsOn), appdef.AppKeycloak, "generic webapp must not depend on keycloak")
	assert.Equal(t, basic.GetHashInput(), kc.GetHashInput(),
		"generic webapp deployment hash must be identical across BASIC and KEYCLOAK modes")
}

// TestKeycloakIntegratedAppsDependOnKeycloak: the proxy (OIDC termination) and
// emodel (keycloak admin ops) declare dependsOn keycloak, but ONLY when keycloak is
// generated (auth mode KEYCLOAK). In BASIC mode keycloak is absent and the dep is
// omitted — so the prune pass never sees a phantom dep, and startup ordering is real.
func TestKeycloakIntegratedAppsDependOnKeycloak(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"gateway": {Image: "registry.citeck.ru/gateway:1.0.0"},
		"emodel":  {Image: "registry.citeck.ru/emodel:1.0.0"},
	}}
	gen := func(auth AuthenticationType) *GenResp {
		cfg := &Config{
			Authentication: AuthenticationProps{Type: auth, Users: []string{"admin"}},
			Proxy:          ProxyProps{Port: 80},
		}
		resp, err := Generate(cfg, bun, nil, SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		return resp
	}

	kc := gen(AuthKeycloak)
	kcProxy := findGeneratedApp(kc, appdef.AppProxy)
	kcEmodel := findGeneratedApp(kc, appdef.AppEmodel)
	require.NotNil(t, kcProxy)
	require.NotNil(t, kcEmodel)
	assert.Contains(t, []string(kcProxy.DependsOn), appdef.AppKeycloak, "proxy must depend on keycloak in KEYCLOAK mode")
	assert.Contains(t, []string(kcEmodel.DependsOn), appdef.AppKeycloak, "emodel must depend on keycloak in KEYCLOAK mode")

	basic := gen(AuthBasic)
	bProxy := findGeneratedApp(basic, appdef.AppProxy)
	bEmodel := findGeneratedApp(basic, appdef.AppEmodel)
	require.NotNil(t, bProxy)
	require.NotNil(t, bEmodel)
	assert.NotContains(t, []string(bProxy.DependsOn), appdef.AppKeycloak, "proxy must not depend on absent keycloak in BASIC mode")
	assert.NotContains(t, []string(bEmodel.DependsOn), appdef.AppKeycloak, "emodel must not depend on absent keycloak in BASIC mode")
}

// TestPruneApps_KeepsBuiltinsWhenDepsPresent is the safety invariant for prune-all:
// in a realistic generation where every built-in's dependency is present (gateway in
// the bundle → proxy's dep satisfied), the prune pass removes NO built-in. If a
// future generator change introduced an unguarded phantom dep on a conditionally-
// generated app, this would go red.
func TestPruneApps_KeepsBuiltinsWhenDepsPresent(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"gateway": {Image: "registry.citeck.ru/gateway:1.0.0"},
		"emodel":  {Image: "registry.citeck.ru/emodel:1.0.0"},
	}}
	resp, err := Generate(basicCfg(), bun, nil, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	for _, name := range []string{appdef.AppProxy, appdef.AppGateway, "emodel", appdef.AppZookeeper, appdef.AppRabbitmq} {
		assert.NotNil(t, findGeneratedApp(resp, name), "built-in %q must not be pruned when its deps are present", name)
	}
}

// TestPruneApps_DropsProxyWithoutGateway documents the accepted behavior: with no
// gateway generated (minimal/bundleless namespace), the proxy hard-depends on the
// absent gateway and is correctly pruned — a proxy fronting nothing serves no purpose.
func TestPruneApps_DropsProxyWithoutGateway(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), &bundle.Def{}, nil, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, appdef.AppProxy),
		"proxy depends on the (absent) gateway and must be pruned")
}

// TestGenerateAdditionalApps_PlatformVars proves the context-aware ${VAR}s
// (platform secrets + web URL + RMQ creds) resolve in an additionalApps env, so a
// config-driven service can integrate with ECOS auth/messaging without hardcoding.
func TestGenerateAdditionalApps_PlatformVars(t *testing.T) {
	config.ResetDesktopMode()
	def := bundle.AdditionalAppProps{
		Name:  "custom-svc",
		Image: "example.com/custom:1",
		Environments: map[string]string{
			"AUTH_JWTSECRET": "${JWT_SECRET}",
			"OIDC_SECRET":    "${OIDC_SECRET}",
			"WEB_URL":        "${WEB_URL}",
			"RMQ_USER":       "${RMQ_USER}",
			"RMQ_PASSWORD":   "${RMQ_PASSWORD}",
			"KK_HOST":        "${KK_HOST}",
			"ADMIN_PASSWORD": "${ADMIN_PASSWORD}",
		},
	}
	ws := wsWithApps([]bundle.AdditionalAppProps{def})
	secrets := SystemSecrets{JWT: "jwt-x", OIDC: "oidc-x", CiteckSA: "sa-x", AdminPassword: "adm-x"}

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, secrets)
	require.NoError(t, err)
	app := findGeneratedApp(resp, "custom-svc")
	require.NotNil(t, app)

	get := func(k string) string { v, _ := app.Environments.Get(k); return v }
	assert.Equal(t, "jwt-x", get("AUTH_JWTSECRET"), "${JWT_SECRET} → Secrets.JWT")
	assert.Equal(t, "oidc-x", get("OIDC_SECRET"))
	assert.Equal(t, "http://localhost", get("WEB_URL"), "${WEB_URL} → ProxyBaseURL")
	assert.Equal(t, CiteckSAUser, get("RMQ_USER"))
	assert.Equal(t, "sa-x", get("RMQ_PASSWORD"), "${RMQ_PASSWORD} → Secrets.CiteckSA")
	assert.Equal(t, KKHost, get("KK_HOST"))
	assert.Equal(t, "adm-x", get("ADMIN_PASSWORD"))
	for _, k := range []string{"AUTH_JWTSECRET", "WEB_URL", "RMQ_PASSWORD"} {
		assert.NotContains(t, get(k), "${", "no literal ${...} may survive")
	}
}

// TestGenerateAdditionalApps_RMQUserGatedOnSecret: when the citeck SA secret is
// unset, ${RMQ_USER} resolves to "" (not the "citeck" user), mirroring the webapp
// infra guard — a service never gets a username with a blank password.
func TestGenerateAdditionalApps_RMQUserGatedOnSecret(t *testing.T) {
	config.ResetDesktopMode()
	def := bundle.AdditionalAppProps{
		Name:         "custom-svc",
		Image:        "example.com/custom:1",
		Environments: map[string]string{"RMQ_USER": "${RMQ_USER}", "RMQ_PASSWORD": "${RMQ_PASSWORD}"},
	}
	ws := wsWithApps([]bundle.AdditionalAppProps{def})

	// No CiteckSA → both empty.
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j"})
	require.NoError(t, err)
	app := findGeneratedApp(resp, "custom-svc")
	require.NotNil(t, app)
	u, _ := app.Environments.Get("RMQ_USER")
	p, _ := app.Environments.Get("RMQ_PASSWORD")
	assert.Empty(t, u, "${RMQ_USER} must be empty when the SA secret is unset")
	assert.Empty(t, p, "${RMQ_PASSWORD} must be empty when the SA secret is unset")

	// With CiteckSA → the SA user + password appear together.
	resp2, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", CiteckSA: "sa-x"})
	require.NoError(t, err)
	app2 := findGeneratedApp(resp2, "custom-svc")
	require.NotNil(t, app2)
	u2, _ := app2.Environments.Get("RMQ_USER")
	p2, _ := app2.Environments.Get("RMQ_PASSWORD")
	assert.Equal(t, CiteckSAUser, u2)
	assert.Equal(t, "sa-x", p2)
}
