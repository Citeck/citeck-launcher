package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// betaBundle is a release that ships emodel and two BETA modules: a webapp
// (ecos-scanner, admitted through the workspace webapps list like any other
// bundle application) and a raw container the workspace declares without an
// image (backup-engine, an additionalApps entry the bundle names the image of).
func betaBundle() *bundle.Def {
	return &bundle.Def{
		Applications: map[string]bundle.AppDef{"emodel": {Image: "core/ecos-model:1.0"}},
		Experimental: map[string]bundle.ExperimentalApp{
			"ecos-scanner":  {AppDef: bundle.AppDef{Image: "core/ecos-scanner:1.0"}, Feature: "documentScanner", Title: "Document scanning"},
			"backup-engine": {AppDef: bundle.AppDef{Image: "core/backup-engine:0.3"}, Feature: "backupEngineV2"},
		},
	}
}

func betaWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{
		Webapps:        []bundle.WebappConfig{{ID: "emodel"}, {ID: "ecos-scanner"}},
		AdditionalApps: []bundle.AdditionalAppProps{{Name: "backup-engine"}},
	}
}

func cfgWithFeatures(features ...string) *Config {
	cfg := basicCfg()
	cfg.ExperimentalFeatures = features
	return cfg
}

func genBeta(t *testing.T, cfg *Config, bun *bundle.Def, opts ...GenerateOpts) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	resp, err := Generate(cfg, bun, betaWorkspace(), SystemSecrets{JWT: "j", OIDC: "o", CiteckSA: "sa"}, opts...)
	require.NoError(t, err)
	return resp
}

func betaApp(apps []appdef.ApplicationDef, name string) *appdef.ApplicationDef {
	for i := range apps {
		if apps[i].Name == name {
			return &apps[i]
		}
	}
	return nil
}

func hashesByName(apps []appdef.ApplicationDef) map[string]string {
	out := make(map[string]string, len(apps))
	for i := range apps {
		out[apps[i].Name] = apps[i].GetHashInput()
	}
	return out
}

// Nothing on: the beta modules are invisible, and the generation is the one a
// bundle WITHOUT the section produces — byte for byte, per app. That is the
// release-safety property: shipping a bundle with an experimental section
// recreates nothing on a stand that opted into nothing.
func TestExperimental_NothingOnGeneratesExactlyTheOldBundle(t *testing.T) {
	with := genBeta(t, basicCfg(), betaBundle())
	plain := betaBundle()
	plain.Experimental = nil
	without := genBeta(t, basicCfg(), plain)

	assert.Nil(t, betaApp(with.Applications, "ecos-scanner"))
	assert.Nil(t, betaApp(with.Applications, "backup-engine"))
	assert.Equal(t, hashesByName(without.Applications), hashesByName(with.Applications))
	for _, a := range with.Applications {
		assert.False(t, a.Experimental, a.Name)
	}
}

// One feature on: its module is generated — as a webapp or from its
// additionalApps declaration alike — carries the beta marker on both the
// baseline and the effective def, and the other feature stays off.
func TestExperimental_OptInGeneratesTheModuleWithTheMarker(t *testing.T) {
	resp := genBeta(t, cfgWithFeatures("documentScanner"), betaBundle())

	scanner := betaApp(resp.Applications, "ecos-scanner")
	require.NotNil(t, scanner, "the opted-in webapp is generated")
	assert.True(t, scanner.Experimental)
	assert.Equal(t, "core/ecos-scanner:1.0", scanner.Image)
	baseline := betaApp(resp.BaselineApplications, "ecos-scanner")
	require.NotNil(t, baseline)
	assert.True(t, baseline.Experimental, "the marker is stamped on the baseline too")

	assert.Nil(t, betaApp(resp.Applications, "backup-engine"), "the other feature is off")
	emodel := betaApp(resp.Applications, "emodel")
	require.NotNil(t, emodel)
	assert.False(t, emodel.Experimental, "a regular application is never marked")

	both := genBeta(t, cfgWithFeatures("documentScanner", "backupEngineV2"), betaBundle())
	backup := betaApp(both.Applications, "backup-engine")
	require.NotNil(t, backup, "a raw additionalApps entry without an image follows the bundle's experimental image")
	assert.True(t, backup.Experimental)
	assert.Equal(t, "core/backup-engine:0.3", backup.Image)
}

// A `citeck edit` patch cannot turn a beta module into a regular one: the
// marker is restored through ApplyAppDefPatch like IsJVM.
func TestExperimental_MarkerSurvivesAnAppPatch(t *testing.T) {
	resp := genBeta(t, cfgWithFeatures("documentScanner"), betaBundle(), GenerateOpts{
		EditedAppPatches: map[string]json.RawMessage{"ecos-scanner": json.RawMessage(`{"image":"core/ecos-scanner:1.1"}`)},
	})
	scanner := betaApp(resp.Applications, "ecos-scanner")
	require.NotNil(t, scanner)
	assert.Equal(t, "core/ecos-scanner:1.1", scanner.Image, "the patch applied")
	assert.True(t, scanner.Experimental, "and the marker stayed")
}

// An id the bundle does not offer is inert: nothing extra is generated and
// nothing fails — it is the stored choice of a graduated or not-yet-shipped
// module, and refusing it would refuse the bundle upgrade that graduated it.
func TestExperimental_AnUnknownFeatureIsInert(t *testing.T) {
	resp := genBeta(t, cfgWithFeatures("graduatedLongAgo"), betaBundle())
	assert.Nil(t, betaApp(resp.Applications, "ecos-scanner"))
	assert.Nil(t, betaApp(resp.Applications, "backup-engine"))
}

// Eligibility must be identical before and after graduation. Opt-in never
// bypasses the workspace's service configuration; the beta marker alone changes.
func TestExperimental_WorkspaceEligibilitySurvivesGraduationAndRollback(t *testing.T) {
	config.ResetDesktopMode()
	for _, registered := range []bool{false, true} {
		name := "unregistered"
		if registered {
			name = "registered"
		}
		t.Run(name, func(t *testing.T) {
			ws := betaWorkspace()
			if !registered {
				ws.Webapps = []bundle.WebappConfig{{ID: "emodel"}}
				ws.AdditionalApps = nil
			}
			stable := betaBundle()
			for app, def := range stable.Experimental {
				stable.Applications[app] = def.AppDef
			}
			stable.Experimental = nil
			cfg := cfgWithFeatures("documentScanner", "backupEngineV2")
			var previous map[string]string
			for _, bun := range []*bundle.Def{betaBundle(), stable, betaBundle()} {
				resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o", CiteckSA: "sa"})
				require.NoError(t, err)
				for _, app := range []string{"ecos-scanner", "backup-engine"} {
					def := betaApp(resp.Applications, app)
					if registered {
						require.NotNil(t, def, app)
						assert.Equal(t, bun.Experimental != nil, def.Experimental, app)
					} else {
						assert.Nil(t, def, "an undeclared service must not bypass the workspace filter: %s", app)
					}
				}
				hashes := hashesByName(resp.Applications)
				if previous != nil {
					assert.Equal(t, previous, hashes, "graduation/rollback alone must not recreate services")
				}
				previous = hashes
			}
		})
	}
}
