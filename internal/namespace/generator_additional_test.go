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
}
