package namespace

import (
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNewNamespaceMongoCompatibility(t *testing.T) {
	for _, tc := range []struct {
		tag     string
		enabled bool
	}{
		{"2.30.7", true}, {"2.32.99", true}, {"2.33.0-rc.1", false},
		{"2.33.0", false}, {"2.33.1", false}, {"2.100.0", false}, {"3.0.0", false},
		{"v2.33.0", false}, {"snapshot", true}, {"latest", true}, {"", true},
		{"2.33.0@sha256:abcdef", true}, {"@sha256:abcdef", true},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			cfg := mongoTestConfig(CurrentAPIVersion(), nil)
			def := eprocBundle()
			def.Applications[appdef.AppEproc] = bundle.AppDef{Image: "registry:5000/ecos-process:" + tc.tag}
			ApplyNewNamespaceMongoDefault(cfg, def)
			data, err := MarshalNamespaceConfig(cfg)
			require.NoError(t, err)
			stored, err := ParseNamespaceConfig(data)
			require.NoError(t, err)
			require.Equal(t, tc.enabled, stored.MongoEnabled())
			resp, err := Generate(stored, def, eprocMongoWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
			require.NoError(t, err)
			require.Equal(t, tc.enabled, appByName(t, resp, appdef.AppMongodb) != nil)
			proc := appByName(t, resp, appdef.AppEproc)
			require.NotNil(t, proc)
			if tc.enabled {
				require.Contains(t, proc.DependsOn, appdef.AppMongodb)
			} else {
				require.NotContains(t, proc.DependsOn, appdef.AppMongodb)
			}
		})
	}
	for _, explicit := range []bool{true, false} {
		cfg := mongoTestConfig(CurrentAPIVersion(), &explicit)
		ApplyNewNamespaceMongoDefault(cfg, eprocBundle())
		require.Equal(t, explicit, cfg.MongoEnabled())
	}
}

func TestEprocMongoIgnoresBuildSuffixes(t *testing.T) {
	for _, suffix := range []string{"-snapshot", "-SNAPSHOT", "-v2", "-RC2", "-rc.1", "-custom-20260922", "_build_7", ".build.7", "+build.7", "RC2"} {
		for _, release := range []string{"2.30.7", "2.32.99", "2.33.0", "2.33.1", "3.0.0"} {
			t.Run(release+suffix, func(t *testing.T) {
				cfg := mongoTestConfig(CurrentAPIVersion(), nil)
				def := eprocBundle()
				def.Applications[appdef.AppEproc] = bundle.AppDef{Image: "registry:5000/ecos-process:" + release + suffix}
				ApplyNewNamespaceMongoDefault(cfg, def)
				require.Equal(t, release == "2.30.7" || release == "2.32.99", cfg.MongoEnabled())
			})
		}
	}
}
