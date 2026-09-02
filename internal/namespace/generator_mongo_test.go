package namespace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// eprocMongoWorkspace mirrors the real workspace config: eproc is the only
// webapp with a mongo datasource, alongside its two postgres ones.
func eprocMongoWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID: appdef.AppEproc,
			DefaultProps: bundle.WebappDefaultProps{
				DataSources: map[string]bundle.DataSourceConfig{
					"eproc":        {URL: "jdbc:postgresql://${PG_HOST}:${PG_PORT}/citeck_eproc"},
					"main-mongodb": {URL: "mongodb://${MONGO_HOST}:${MONGO_PORT}/citeck_eproc"},
				},
			},
		}},
	}
}

func eprocBundle() *bundle.Def {
	return &bundle.Def{
		Applications: map[string]bundle.AppDef{
			appdef.AppEproc: {Image: "nexus.citeck.ru/eproc:1.0"},
		},
	}
}

func mongoTestConfig(apiVersion string, enabled *bool) *Config {
	return &Config{
		APIVersion:     apiVersion,
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		MongoDB:        MongoDbProps{Enabled: enabled},
	}
}

func generateEproc(t *testing.T, cfg *Config) *GenResp {
	t.Helper()
	resp, err := Generate(cfg, eprocBundle(), eprocMongoWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	return resp
}

func appByName(t *testing.T, resp *GenResp, name string) *appdef.ApplicationDef {
	t.Helper()
	for i := range resp.Applications {
		if resp.Applications[i].Name == name {
			return &resp.Applications[i]
		}
	}
	return nil
}

// A namespace that predates the flag must keep its database. The key is absent
// from every namespace.yml written before this release, so reading that absence
// as "disabled" would delete the mongo container out from under a running stand
// on the first reload after an upgrade — silently, since nothing else changed.
func TestMongoStaysOnANamespaceThatPredatesTheFlag(t *testing.T) {
	resp := generateEproc(t, mongoTestConfig("", nil))

	require.NotNil(t, appByName(t, resp, appdef.AppMongodb), "mongo must survive on a generation-1 namespace")

	eproc := appByName(t, resp, appdef.AppEproc)
	require.NotNil(t, eproc)
	require.Contains(t, eproc.DependsOn, appdef.AppMongodb)

	props := string(resp.Files["app/eproc/props/application-launcher.yml"])
	require.Contains(t, props, "mongodb://mongo:27017/citeck_eproc")
	require.NotContains(t, props, "ecos-process", "nothing may switch mongo off while the container is there")
}

// A namespace created at the current generation runs without mongo: no
// container, and — just as important — nothing left behind pointing at it.
func TestNewNamespacesAreGeneratedWithoutMongo(t *testing.T) {
	resp := generateEproc(t, mongoTestConfig(CurrentAPIVersion(), nil))

	require.Nil(t, appByName(t, resp, appdef.AppMongodb), "no mongo container on a current-generation namespace")

	eproc := appByName(t, resp, appdef.AppEproc)
	require.NotNil(t, eproc)
	// A leftover depends_on parks the webapp in waitForDeps forever.
	require.NotContains(t, eproc.DependsOn, appdef.AppMongodb)
	require.False(t, eproc.Environments.Has("SPRING_DATA_MONGODB_URI"),
		"no URI may point at a container that is gone")

	props := string(resp.Files["app/eproc/props/application-launcher.yml"])
	require.NotContains(t, props, "mongodb://")
	// eproc opens its mongo client from its own switch, not from the datasource
	// list, so dropping the datasource alone still leaves it failing at startup.
	require.Contains(t, strings.ReplaceAll(props, " ", ""), "ecos-process:\nmongo:\nenabled:false")

	require.NotContains(t, resp.CloudConfig[appdef.AppEproc], "ecos.webapp.dataSources.main-mongodb.url")
}

// The stored flag is authoritative in both directions, whatever the generation
// says — a hand-edited YAML (or a workspace template) must not be second-guessed.
func TestStoredMongoFlagWinsOverTheGenerationDefault(t *testing.T) {
	on, off := true, false

	resp := generateEproc(t, mongoTestConfig(CurrentAPIVersion(), &on))
	require.NotNil(t, appByName(t, resp, appdef.AppMongodb), "an explicit enabled:true keeps mongo on a new namespace")

	resp = generateEproc(t, mongoTestConfig("", &off))
	require.Nil(t, appByName(t, resp, appdef.AppMongodb), "an explicit enabled:false removes it from an old one")
}

// The version parser decides the default for every namespace that has no flag,
// so its edge cases are the blast radius.
func TestConfigVersionDefaults(t *testing.T) {
	require.Equal(t, 1, (&Config{}).Version(), "absent apiVersion is generation 1")
	require.Equal(t, 1, (&Config{APIVersion: "v1"}).Version())
	require.Equal(t, 2, (&Config{APIVersion: "v2"}).Version())
	// Anything unreadable must degrade to the OLD generation: guessing "new"
	// would strip mongo from a namespace we failed to understand.
	require.Equal(t, 1, (&Config{APIVersion: "banana"}).Version())
	require.Equal(t, 1, (&Config{APIVersion: "v0"}).Version())

	require.True(t, (&Config{}).MongoEnabled())
	require.False(t, (&Config{APIVersion: CurrentAPIVersion()}).MongoEnabled())
}

// The flag has to survive a YAML round-trip in all three states, since that is
// how it reaches the generator on every load.
func TestMongoFlagRoundTripsThroughYAML(t *testing.T) {
	base := "id: n\nname: n\nproxy:\n  port: 80\n"

	cfg, err := ParseNamespaceConfig([]byte(base))
	require.NoError(t, err)
	require.Nil(t, cfg.MongoDB.Enabled, "an absent key must stay absent, not become false")

	cfg, err = ParseNamespaceConfig([]byte(base + "mongodb:\n  enabled: false\n"))
	require.NoError(t, err)
	require.NotNil(t, cfg.MongoDB.Enabled)
	require.False(t, *cfg.MongoDB.Enabled)

	out, err := MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	require.Contains(t, string(out), "enabled: false")

	// A config with no flag must not gain one on save — that would freeze the
	// generation default into the file and defeat the version.
	cfg, err = ParseNamespaceConfig([]byte(base))
	require.NoError(t, err)
	out, err = MarshalNamespaceConfig(cfg)
	require.NoError(t, err)
	require.Contains(t, string(out), "mongodb:\n  image:", "the mongodb block must carry no enabled key")
}
