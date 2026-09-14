package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// The point of the `dependencies:` section is that an image named there is
// invisible to a launcher with no dependency gate — but fully effective here.
func TestBundleDependenciesSectionNamesTheImage(t *testing.T) {
	bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:17.11"},
	}}
	assert.Equal(t, "postgres:17.11", appByName(t, generateWithPins(t, bun, nil), appdef.AppPostgres).Image)
}

// Resolution order: Dependencies → Applications → the generator's own fallback.
// A bundle in transition may carry both (the top-level entry for launchers that
// have no gate, the new section for those that do) and the new section is the
// one this launcher must obey — otherwise the section could never be used to
// raise a version at all.
func TestBundleDependenciesWinOverATopLevelEntry(t *testing.T) {
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:17.9"}},
		Dependencies: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:17.11"}},
	}
	assert.Equal(t, "postgres:17.11", appByName(t, generateWithPins(t, bun, nil), appdef.AppPostgres).Image)
}

// The other half of the contract: a bundle that names an image only at the top
// level behaves EXACTLY as it does today. Every bundle in the field is this one.
func TestATopLevelEntryStillNamesTheImage(t *testing.T) {
	// 17.9 rather than 17.5: the generator's OWN fallback is postgres:17.5, so a
	// top-level entry naming it would pass even with the top-level lookup deleted.
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:17.9"},
	}}
	assert.Equal(t, "postgres:17.9", appByName(t, generateWithPins(t, bun, nil), appdef.AppPostgres).Image)
}

// The section is not postgres-shaped: it covers ANY app name, so a bundle author
// can hide every third-party image there. mailpit does not go through the
// dependency gate at all, which is exactly why it is the one worth pinning.
func TestBundleDependenciesCoverAnUngatedThirdPartyImage(t *testing.T) {
	bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		appdef.AppMailpit: {Image: "axllent/mailpit:v1.99.0"},
	}}
	assert.Equal(t, "axllent/mailpit:v1.99.0", appByName(t, generateWithPins(t, bun, nil), appdef.AppMailpit).Image)
}

// Mongo uses the bundle image above namespace defaults; data pins still apply afterwards.
func TestMongoImagePrecedence(t *testing.T) {
	depsBundle := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		appdef.AppMongodb: {Image: "mongo:4.4.29"},
	}}
	t.Run("bundle wins over namespace config", func(t *testing.T) {
		cfg := depsTestConfig()
		cfg.MongoDB.Image = "mongo:4.2.24"
		assert.Equal(t, "mongo:4.4.29",
			appByName(t, generateCfgWithStates(t, cfg, depsBundle, nil), appdef.AppMongodb).Image)
	})
	t.Run("the bundle section wins over the literal default", func(t *testing.T) {
		assert.Equal(t, "mongo:4.4.29",
			appByName(t, generateWithPins(t, depsBundle, nil), appdef.AppMongodb).Image)
	})
	t.Run("neither ⇒ the launcher's own default", func(t *testing.T) {
		assert.Equal(t, "mongo:4.0.2",
			appByName(t, generateWithPins(t, nil, nil), appdef.AppMongodb).Image)
	})
	t.Run("a top-level bundle entry beats the default", func(t *testing.T) {
		bun := &bundle.Def{Applications: map[string]bundle.AppDef{
			appdef.AppMongodb: {Image: "mongo:9.9.9"},
		}}
		assert.Equal(t, "mongo:9.9.9",
			appByName(t, generateWithPins(t, bun, nil), appdef.AppMongodb).Image,
			"the bundle selects the image")
	})
}

// Moving an image into the new section changes WHERE the candidate is read
// from, never what the gate does with it: a breaking bump is still held back on
// the pinned image and still reported as the available upgrade.
func TestADependencySectionImageIsStillHeldBackByThePin(t *testing.T) {
	bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	assert.Equal(t, "postgres:17.5", appByName(t, resp, appdef.AppPostgres).Image,
		"the container must keep running the pin")
	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up, "the hold must be reported")
	assert.Equal(t, "postgres:17.5", up.From)
	assert.Equal(t, "postgres:18", up.To)
	assert.Equal(t, DependencyGen{Effective: "postgres:17.5", Candidate: "postgres:18"},
		resp.Dependencies[deps.Postgres])
}
