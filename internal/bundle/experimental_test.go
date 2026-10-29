package bundle

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseTestBundleAliased is parseTestBundle with an alias map, for the one
// case where a module is declared under its Helm-style name.
func parseTestBundleAliased(t *testing.T, yml string, aliasMap map[string]string) *Def {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yml), 0o600))
	def, err := parseBundleFile(path, "test", aliasMap, map[string]string{"core": "nexus.citeck.ru"}, nil)
	require.NoError(t, err)
	return def
}

// The section is read, and kept APART from the applications: a module nobody
// switched on must not reach the webapp loop, and IsEmpty must keep meaning
// "no Citeck apps". Both image forms work, the imageRepos rewriting applies,
// and `feature` defaults to the module's own name.
func TestParseBundleFile_ExperimentalSectionIsReadAndKeptApart(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  ecos-scanner:
    image:
      repository: core/ecos-scanner
      tag: "1.0"
    feature: documentScanner
    title: "  Document scanning  "
    description: New OCR service. Beta.
  scanner-postgres:
    image: postgres:17.11
    feature: documentScanner
  backup-engine:
    image: core/backup-engine:0.3.0
`)
	require.Len(t, def.Experimental, 3)
	assert.Equal(t, ExperimentalApp{
		AppDef:      AppDef{Image: "nexus.citeck.ru/ecos-scanner:1.0"},
		Feature:     "documentScanner",
		Title:       "Document scanning",
		Description: "New OCR service. Beta.",
	}, def.Experimental["ecos-scanner"])
	assert.Equal(t, "postgres:17.11", def.Experimental["scanner-postgres"].Image)
	assert.Equal(t, "documentScanner", def.Experimental["scanner-postgres"].Feature)
	assert.Equal(t, "backup-engine", def.Experimental["backup-engine"].Feature,
		"an entry with no feature is a feature of its own")

	assert.Equal(t, map[string]AppDef{"EcosModelApp": {Image: "nexus.citeck.ru/ecos-model:1.0"}}, def.Applications,
		"beta modules are not applications until a namespace opts in")
	assert.NotContains(t, def.Applications, "experimental",
		"the section key is not an application")
	assert.False(t, def.IsEmpty())
}

// A bundle with nothing but beta modules has nothing of the product in it for
// a namespace that opted into none — the same verdict a dependencies-only
// bundle gets, and the one that raises the bundle-error banner.
func TestParseBundleFile_ExperimentalOnlyBundleIsEmpty(t *testing.T) {
	def := parseTestBundle(t, `
experimental:
  ecos-scanner:
    image: core/ecos-scanner:1.0
`)
	assert.True(t, def.IsEmpty())
	assert.Len(t, def.Experimental, 1)
}

// An entry with no image, or with one the launcher cannot read, is dropped and
// costs nothing but itself.
func TestParseBundleFile_ExperimentalEntriesWithoutAReadableImageAreDropped(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  no-image:
    feature: x
    title: Nothing to run
  half-image:
    image:
      repository: core/half
  ok:
    image: core/ok:1
`)
	assert.Equal(t, []string{"ok"}, slices.Sorted(maps.Keys(def.Experimental)))
	assert.Contains(t, def.Applications, "EcosModelApp")
}

// A section that is not a mapping costs the section, never the bundle.
func TestParseBundleFile_ExperimentalWrongShapeIsIgnored(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  - ecos-scanner
`)
	assert.Nil(t, def.Experimental)
	assert.Contains(t, def.Applications, "EcosModelApp")
}

// One name, one definition: the top level is what every launcher agrees on,
// so a module that is also a regular application is not a module.
func TestParseBundleFile_ExperimentalCollidingWithARegularAppLosesToIt(t *testing.T) {
	def := parseTestBundle(t, `
scanner:
  image: core/scanner:2.0
experimental:
  scanner:
    image: core/scanner:3.0-beta
`)
	assert.Nil(t, def.Experimental)
	assert.Equal(t, "nexus.citeck.ru/scanner:2.0", def.Applications["scanner"].Image)
}

// The alias map applies as it does to a regular application, so a module
// declared under its Helm-style name lands under the launcher's.
func TestParseBundleFile_ExperimentalAliasMapApplies(t *testing.T) {
	def := parseTestBundleAliased(t, `
experimental:
  EcosScannerApp:
    image: core/ecos-scanner:1.0
`, map[string]string{"EcosScannerApp": "scanner"})
	assert.Equal(t, []string{"scanner"}, slices.Sorted(maps.Keys(def.Experimental)))
	assert.Equal(t, "scanner", def.Experimental["scanner"].Feature,
		"the default feature is the CANONICAL name")
}

// Features are the switches: one per feature id, sorted, each with its apps
// sorted, titled by the first app that has a title and by the id otherwise.
func TestDef_ExperimentalFeaturesGroupsByFeature(t *testing.T) {
	def := &Def{Experimental: map[string]ExperimentalApp{
		"scanner-postgres": {AppDef: AppDef{Image: "postgres:17"}, Feature: "documentScanner", Description: "db only"},
		"ecos-scanner":     {AppDef: AppDef{Image: "x:1"}, Feature: "documentScanner", Title: "Document scanning"},
		"backup-engine":    {AppDef: AppDef{Image: "y:1"}, Feature: "backupEngineV2"},
	}}
	assert.Equal(t, []ExperimentalFeature{
		{ID: "backupEngineV2", Title: "backupEngineV2", Apps: []string{"backup-engine"}},
		{ID: "documentScanner", Title: "Document scanning", Description: "db only", Apps: []string{"ecos-scanner", "scanner-postgres"}},
	}, def.ExperimentalFeatures())

	assert.Nil(t, (&Def{}).ExperimentalFeatures())
	var nilDef *Def
	assert.Nil(t, nilDef.ExperimentalFeatures())
}

// The effective bundle admits exactly the apps of the enabled features, never
// mutates the receiver, and is the receiver itself when nothing is admitted —
// which is what keeps a namespace with no features on generating from the Def
// it always did.
func TestDef_WithExperimentalEnabled(t *testing.T) {
	def := &Def{
		Applications: map[string]AppDef{"emodel": {Image: "e:1"}, "scanner": {Image: "top:1"}},
		Experimental: map[string]ExperimentalApp{
			"ecos-scanner":     {AppDef: AppDef{Image: "s:1"}, Feature: "documentScanner"},
			"scanner-postgres": {AppDef: AppDef{Image: "postgres:17"}, Feature: "documentScanner"},
			"backup-engine":    {AppDef: AppDef{Image: "b:1"}, Feature: "backupEngineV2"},
			"scanner":          {AppDef: AppDef{Image: "beta:1"}, Feature: "documentScanner"},
		},
	}

	t.Run("nothing enabled is the same Def", func(t *testing.T) {
		eff, admitted := def.WithExperimentalEnabled(nil)
		assert.Same(t, def, eff)
		assert.Nil(t, admitted)
		eff, admitted = def.WithExperimentalEnabled(map[string]bool{"unknown": true})
		assert.Same(t, def, eff)
		assert.Nil(t, admitted)
	})

	t.Run("one feature admits its apps and nothing else", func(t *testing.T) {
		eff, admitted := def.WithExperimentalEnabled(map[string]bool{"documentScanner": true})
		assert.Equal(t, map[string]string{"ecos-scanner": "documentScanner", "scanner-postgres": "documentScanner"}, admitted,
			"backup-engine is off; `scanner` loses to the regular application")
		assert.Equal(t, map[string]AppDef{
			"emodel":           {Image: "e:1"},
			"scanner":          {Image: "top:1"},
			"ecos-scanner":     {Image: "s:1"},
			"scanner-postgres": {Image: "postgres:17"},
		}, eff.Applications)
		assert.Len(t, def.Applications, 2, "the receiver is untouched")
		assert.Equal(t, def.Experimental, eff.Experimental, "the section rides along unchanged")
	})

	t.Run("nil receiver", func(t *testing.T) {
		var nilDef *Def
		eff, admitted := nilDef.WithExperimentalEnabled(map[string]bool{"x": true})
		assert.Nil(t, eff)
		assert.Nil(t, admitted)
	})
}

func TestParseBundleFile_ExperimentalInvalidIDsAreNotOffered(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  scanner:
    image: core/scanner:1
    feature: document/scanner
  '-invalid-default':
    image: core/scanner:1
  valid:
    image: core/valid:1
    feature: ' documentScanner '
`)
	require.Len(t, def.Experimental, 1)
	require.Len(t, def.ExperimentalFeatures(), 1)
	assert.Equal(t, "documentScanner", def.ExperimentalFeatures()[0].ID)
	assert.Contains(t, def.Applications, "EcosModelApp", "bad metadata must not discard the bundle")
}
