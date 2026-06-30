package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ediSimAdditionalApp is the config-only definition of the EDI simulator — the
// motivating example: a custom Go service added to a namespace by configuration
// alone, with no dedicated launcher generator.
func ediSimAdditionalApp() AdditionalAppProps {
	return AdditionalAppProps{
		Name:           "edi-sim",
		Image:          "registry.citeck.ru/community/citeck-edi-sim:0.1.0",
		NetworkAliases: []string{"EcosEdiSimApp"},
		DependsOn:      []string{appdef.AppZookeeper},
		Environments: map[string]string{
			"ZOOKEEPER_HOSTS": "${ZK_HOST}:${ZK_PORT}",
			"DISCOVERY_APPNAME": "edi-sim",
		},
		Ports:         []string{"8080"},
		LivenessProbe: &appdef.AppProbeDef{HTTP: &appdef.HTTPProbeDef{Path: "/health", Port: 8080}},
	}
}

func TestGenerateAdditionalApps_EdiSim(t *testing.T) {
	config.ResetDesktopMode() // server mode: only proxy publishes ports
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		AdditionalApps: []AdditionalAppProps{ediSimAdditionalApp()},
	}

	resp, err := Generate(cfg, &bundle.Def{}, nil, SystemSecrets{JWT: "j", OIDC: "o"})
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
	def.Enabled = new(false)
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		AdditionalApps: []AdditionalAppProps{def},
	}
	resp, err := Generate(cfg, &bundle.Def{}, nil, SystemSecrets{JWT: "j", OIDC: "o"})
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
	def := AdditionalAppProps{
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
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		AdditionalApps: []AdditionalAppProps{def},
	}
	require.NoError(t, ValidateNamespaceConfig(cfg))

	resp, err := Generate(cfg, &bundle.Def{}, nil, SystemSecrets{JWT: "j", OIDC: "o"})
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
	def := AdditionalAppProps{
		Name:  "custom-svc",
		Image: "core/citeck-edi-sim:0.1.0-SNAPSHOT",
		InitContainers: []appdef.InitContainerDef{
			{Image: "enterprise/wait-for:1.0"},
		},
	}
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		AdditionalApps: []AdditionalAppProps{def},
	}
	wsCfg := &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
		{ID: "core", URL: "nexus.citeck.ru"},
		{ID: "enterprise", URL: "enterprise-registry.citeck.ru"},
	}}

	resp, err := Generate(cfg, &bundle.Def{}, wsCfg, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	app := findGeneratedApp(resp, "custom-svc")
	require.NotNil(t, app)
	assert.Equal(t, "nexus.citeck.ru/citeck-edi-sim:0.1.0-SNAPSHOT", app.Image, "app image prefix resolved via imageRepos")
	require.Len(t, app.InitContainers, 1)
	assert.Equal(t, "enterprise-registry.citeck.ru/wait-for:1.0", app.InitContainers[0].Image, "init-container image prefix resolved via imageRepos")
}

func TestValidateAdditionalApps(t *testing.T) {
	base := func(apps ...AdditionalAppProps) *Config {
		return &Config{
			Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
			Proxy:          ProxyProps{Port: 80},
			AdditionalApps: apps,
		}
	}

	require.NoError(t, ValidateNamespaceConfig(base(ediSimAdditionalApp())))

	require.Error(t, ValidateNamespaceConfig(base(AdditionalAppProps{Image: "x:1"})), "missing name")
	require.Error(t, ValidateNamespaceConfig(base(AdditionalAppProps{Name: "x"})), "missing image")
	require.Error(t, ValidateNamespaceConfig(base(
		AdditionalAppProps{Name: appdef.AppZookeeper, Image: "x:1"})), "reserved name")
	require.Error(t, ValidateNamespaceConfig(base(
		AdditionalAppProps{Name: "dup", Image: "a:1"},
		AdditionalAppProps{Name: "dup", Image: "b:1"})), "duplicate name")
	require.Error(t, ValidateNamespaceConfig(base(AdditionalAppProps{
		Name: "x", Image: "x:1",
		InitContainers: []appdef.InitContainerDef{{Image: ""}}})), "init container without image")
	require.Error(t, ValidateNamespaceConfig(base(AdditionalAppProps{
		Name: "x", Image: "x:1", StopTimeout: -1})), "negative stopTimeout")
}
