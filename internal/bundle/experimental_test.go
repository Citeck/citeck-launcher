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

// The section is read, keyed by FEATURE, and kept APART from the applications:
// a module nobody switched on must not reach the webapp loop, and IsEmpty must
// keep meaning "no Citeck apps". Both image forms work, the imageRepos
// rewriting applies, the words are the feature's, and a feature with no words
// is still a feature.
func TestParseBundleFile_ExperimentalSectionIsReadByFeatureAndKeptApart(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  documentScanner:
    title: "  Document scanning  "
    description:
      en: New OCR service. Beta.
      ru: Новый сервис распознавания. Beta.
    apps:
      citeck-scanner:
        image:
          repository: core/citeck-scanner
          tag: "1.0"
      scanner-postgres:
        image: postgres:17.11
  backupEngineV2:
    apps:
      backup-engine:
        image: core/backup-engine:0.3.0
`)
	require.Len(t, def.Experimental, 2)
	assert.Equal(t, ExperimentalFeatureDef{
		Title:       PlainText("Document scanning"),
		Description: LocalizedText{"en": "New OCR service. Beta.", "ru": "Новый сервис распознавания. Beta."},
		Apps: map[string]AppDef{
			"citeck-scanner":   {Image: "nexus.citeck.ru/citeck-scanner:1.0"},
			"scanner-postgres": {Image: "postgres:17.11"},
		},
	}, def.Experimental["documentScanner"])
	assert.Equal(t, ExperimentalFeatureDef{
		Apps: map[string]AppDef{"backup-engine": {Image: "nexus.citeck.ru/backup-engine:0.3.0"}},
	}, def.Experimental["backupEngineV2"], "no title, no description: the feature is its id")

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
  documentScanner:
    apps:
      citeck-scanner:
        image: core/citeck-scanner:1.0
`)
	assert.True(t, def.IsEmpty())
	assert.Len(t, def.Experimental, 1)
}

// An app with no image, or with one the launcher cannot read, is dropped and
// costs nothing but itself; a feature left with nothing to switch on is
// dropped too, as are a feature that is not a mapping, one without `apps:`,
// and one whose id a namespace could not store.
func TestParseBundleFile_ExperimentalUnreadablePiecesAreDroppedAlone(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  documentScanner:
    title: Scanning
    apps:
      no-image:
        title: Nothing to run
      half-image:
        image:
          repository: core/half
      ok:
        image: core/ok:1
  emptyFeature:
    title: Nothing to switch on
    apps:
      no-image: {}
  noApps:
    title: A feature without modules
  notAMapping: backup-engine
  "bad id":
    apps:
      x:
        image: core/x:1
`)
	assert.Equal(t, []string{"documentScanner"}, slices.Sorted(maps.Keys(def.Experimental)))
	assert.Equal(t, []string{"ok"}, slices.Sorted(maps.Keys(def.Experimental["documentScanner"].Apps)))
	assert.Contains(t, def.Applications, "EcosModelApp")
}

// A section that is not a mapping costs the section, never the bundle.
func TestParseBundleFile_ExperimentalWrongShapeIsIgnored(t *testing.T) {
	def := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
experimental:
  - documentScanner
`)
	assert.Nil(t, def.Experimental)
	assert.Contains(t, def.Applications, "EcosModelApp")
}

// One name, one definition: the top level is what every launcher agrees on,
// so a module that is also a regular application is not a module — and a
// feature that named nothing else is not a feature.
func TestParseBundleFile_ExperimentalCollidingWithARegularAppLosesToIt(t *testing.T) {
	def := parseTestBundle(t, `
scanner:
  image: core/scanner:2.0
experimental:
  newScanner:
    apps:
      scanner:
        image: core/scanner:3.0-beta
`)
	assert.Nil(t, def.Experimental)
	assert.Equal(t, "nexus.citeck.ru/scanner:2.0", def.Applications["scanner"].Image)
}

// One container has one switch: an app two features both name belongs to the
// first one in document order, and the second feature keeps its other apps.
func TestParseBundleFile_ExperimentalAppNamedTwiceKeepsItsFirstFeature(t *testing.T) {
	def := parseTestBundle(t, `
experimental:
  documentScanner:
    apps:
      scanner-postgres:
        image: postgres:17.11
      citeck-scanner:
        image: core/citeck-scanner:1.0
  backupEngineV2:
    apps:
      scanner-postgres:
        image: postgres:18
      backup-engine:
        image: core/backup-engine:0.3
`)
	assert.Equal(t, []string{"citeck-scanner", "scanner-postgres"}, slices.Sorted(maps.Keys(def.Experimental["documentScanner"].Apps)))
	assert.Equal(t, []string{"backup-engine"}, slices.Sorted(maps.Keys(def.Experimental["backupEngineV2"].Apps)))
	assert.Equal(t, "postgres:17.11", def.Experimental["documentScanner"].Apps["scanner-postgres"].Image)
}

// The alias map applies as it does to a regular application, so a module
// declared under its Helm-style name lands under the launcher's.
func TestParseBundleFile_ExperimentalAliasMapApplies(t *testing.T) {
	def := parseTestBundleAliased(t, `
experimental:
  documentScanner:
    apps:
      CiteckScannerApp:
        image: core/citeck-scanner:1.0
`, map[string]string{"CiteckScannerApp": "scanner"})
	assert.Equal(t, []string{"scanner"}, slices.Sorted(maps.Keys(def.Experimental["documentScanner"].Apps)))
}

// Features are the switches: one per feature id, sorted, each with its apps
// sorted; the author's words ride along unrendered, and a feature with no
// apps is not an offer.
func TestDef_ExperimentalFeaturesListsTheOffers(t *testing.T) {
	def := &Def{Experimental: map[string]ExperimentalFeatureDef{
		"documentScanner": {
			Title:       PlainText("Document scanning"),
			Description: LocalizedText{"ru": "db only"},
			Apps:        map[string]AppDef{"scanner-postgres": {Image: "postgres:17"}, "citeck-scanner": {Image: "x:1"}},
		},
		"backupEngineV2": {Apps: map[string]AppDef{"backup-engine": {Image: "y:1"}}},
		"nothing":        {Title: PlainText("Nothing to switch on")},
	}}
	assert.Equal(t, []ExperimentalFeature{
		{ID: "backupEngineV2", Apps: []string{"backup-engine"}},
		{ID: "documentScanner", Title: PlainText("Document scanning"), Description: LocalizedText{"ru": "db only"},
			Apps: []string{"citeck-scanner", "scanner-postgres"}},
	}, def.ExperimentalFeatures())

	assert.Nil(t, (&Def{}).ExperimentalFeatures())
	var nilDef *Def
	assert.Nil(t, nilDef.ExperimentalFeatures())
}

// TitleFor falls back to the id, DescriptionFor to nothing; both pick the
// reader's language through LocalizedText.
func TestExperimentalFeature_WordsForAReader(t *testing.T) {
	f := ExperimentalFeature{ID: "documentScanner",
		Title:       LocalizedText{"en": "Document scanning", "ru": "Сканирование документов"},
		Description: LocalizedText{"en": "Beta."}}
	assert.Equal(t, "Сканирование документов", f.TitleFor("ru"))
	assert.Equal(t, "Document scanning", f.TitleFor("de"), "English before any other language")
	assert.Equal(t, "Beta.", f.DescriptionFor("ru"))

	bare := ExperimentalFeature{ID: "backupEngineV2"}
	assert.Equal(t, "backupEngineV2", bare.TitleFor("ru"), "no title falls back to the id")
	assert.Empty(t, bare.DescriptionFor("ru"))
}

// The grammar is one for the bundle key and the namespace.yml value.
func TestIsValidExperimentalFeatureID(t *testing.T) {
	for _, ok := range []string{"documentScanner", "backup-engine_v2.1", "x"} {
		assert.True(t, IsValidExperimentalFeatureID(ok), ok)
	}
	for _, bad := range []string{"", " ", "bad id", "-leading", ".leading", "ü"} {
		assert.False(t, IsValidExperimentalFeatureID(bad), bad)
	}
}

// The effective bundle admits exactly the apps of the enabled features, never
// mutates the receiver, and is the receiver itself when nothing is admitted —
// which is what keeps a namespace with no features on generating from the Def
// it always did.
func TestDef_WithExperimentalEnabled(t *testing.T) {
	def := &Def{
		Applications: map[string]AppDef{"emodel": {Image: "e:1"}, "scanner": {Image: "top:1"}},
		Experimental: map[string]ExperimentalFeatureDef{
			"documentScanner": {Apps: map[string]AppDef{
				"citeck-scanner":   {Image: "s:1"},
				"scanner-postgres": {Image: "postgres:17"},
				"scanner":          {Image: "beta:1"},
			}},
			"backupEngineV2": {Apps: map[string]AppDef{
				"backup-engine":    {Image: "b:1"},
				"scanner-postgres": {Image: "postgres:18"},
			}},
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
		assert.Equal(t, map[string]string{"citeck-scanner": "documentScanner", "scanner-postgres": "documentScanner"}, admitted,
			"backup-engine is off; `scanner` loses to the regular application")
		assert.Equal(t, map[string]AppDef{
			"emodel":           {Image: "e:1"},
			"scanner":          {Image: "top:1"},
			"citeck-scanner":   {Image: "s:1"},
			"scanner-postgres": {Image: "postgres:17"},
		}, eff.Applications)
		assert.Len(t, def.Applications, 2, "the receiver is untouched")
		assert.Equal(t, def.Experimental, eff.Experimental, "the section rides along unchanged")
	})

	t.Run("an app two enabled features name belongs to the first by id", func(t *testing.T) {
		eff, admitted := def.WithExperimentalEnabled(map[string]bool{"documentScanner": true, "backupEngineV2": true})
		assert.Equal(t, "backupEngineV2", admitted["scanner-postgres"])
		assert.Equal(t, "postgres:18", eff.Applications["scanner-postgres"].Image)
		assert.Equal(t, "documentScanner", admitted["citeck-scanner"])
		assert.Equal(t, "backupEngineV2", admitted["backup-engine"])
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
  'document/scanner':
    apps:
      scanner:
        image: core/scanner:1
  '-invalid':
    apps:
      invalid:
        image: core/invalid:1
  ' documentScanner ':
    apps:
      valid:
        image: core/valid:1
`)
	require.Len(t, def.Experimental, 1)
	require.Len(t, def.ExperimentalFeatures(), 1)
	assert.Equal(t, "documentScanner", def.ExperimentalFeatures()[0].ID)
	assert.Contains(t, def.Applications, "EcosModelApp", "bad metadata must not discard the bundle")
}
