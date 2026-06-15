package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDisplayBundleRef(t *testing.T) {
	cached := &bundle.Def{Key: bundle.Key{Version: "2026.3-RC1"}}

	// LATEST + cached resolved bundle → show the concrete version.
	assert.Equal(t, "develop:2026.3-RC1",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, cached))

	// LATEST is matched case-insensitively.
	assert.Equal(t, "develop:2026.3-RC1",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "latest"}, cached))

	// LATEST with no cached bundle → raw ref unchanged (graceful fallback).
	assert.Equal(t, "develop:LATEST",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, nil))

	// LATEST with an empty cached version → raw ref unchanged.
	assert.Equal(t, "develop:LATEST",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "LATEST"}, &bundle.Def{}))

	// A concrete key is shown as-is, even when a cached bundle is present.
	assert.Equal(t, "develop:2025.12",
		ResolveDisplayBundleRef(bundle.Ref{Repo: "develop", Key: "2025.12"}, cached))
}

// TestToNamespaceDtoInitProgress pins the AppDto init-progress mapping:
// InitStep/InitTotal/InitName are populated only while the app is STARTING
// with the init phase active, and cleared everywhere else (T12 phase-done,
// post-STARTING statuses).
func TestToNamespaceDtoInitProgress(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())

	def := simpleApp("eapps", "registry.citeck.ru/citeck/eapps:1")
	def.InitContainers = []appdef.InitContainerDef{
		{Image: "registry.citeck.ru/citeck/ecos-app-x:1.2.3"},
		{Image: "init-b:2"},
	}

	// State machine snapshot: init phase active, first init container running.
	// Direct field writes mirror what beginStartingUnderLock stamps (the test
	// runtime has no loop goroutine, so the write is race-free).
	r.mu.Lock()
	r.apps["eapps"] = &AppRuntime{
		Name: "eapps", Status: AppStatusStarting, Def: def,
		initStepIdx: 0, initActive: true,
	}
	r.mu.Unlock()

	dto := r.ToNamespaceDto()
	require.Len(t, dto.Apps, 1)
	app := dto.Apps[0]
	assert.Equal(t, 1, app.InitStep, "first init container → 1-based step 1")
	assert.Equal(t, 2, app.InitTotal)
	assert.Equal(t, "ecos-app-x", app.InitName, "step name = image basename without registry/tag")

	// T11: advance to the second init container.
	r.mu.Lock()
	r.apps["eapps"].initStepIdx = 1
	r.mu.Unlock()
	app = r.ToNamespaceDto().Apps[0]
	assert.Equal(t, 2, app.InitStep)
	assert.Equal(t, 2, app.InitTotal)
	assert.Equal(t, "init-b", app.InitName)

	// T12: init phase done — fields cleared even though status stays STARTING
	// (main container start / startup probe still in flight).
	r.mu.Lock()
	r.apps["eapps"].initActive = false
	r.apps["eapps"].initStepIdx = 0
	r.mu.Unlock()
	app = r.ToNamespaceDto().Apps[0]
	assert.Zero(t, app.InitStep)
	assert.Zero(t, app.InitTotal)
	assert.Empty(t, app.InitName)

	// Defensive gate: even with a stale initActive flag, a non-STARTING status
	// must never surface init progress.
	r.mu.Lock()
	r.apps["eapps"].initActive = true
	r.apps["eapps"].Status = AppStatusRunning
	r.mu.Unlock()
	app = r.ToNamespaceDto().Apps[0]
	assert.Zero(t, app.InitStep)
	assert.Zero(t, app.InitTotal)
	assert.Empty(t, app.InitName)
}

func TestInitStepDisplayName(t *testing.T) {
	assert.Equal(t, "ecos-app-x", initStepDisplayName("registry.citeck.ru/citeck/ecos-app-x:1.2.3"))
	assert.Equal(t, "init-b", initStepDisplayName("init-b:2"))
	assert.Equal(t, "plain", initStepDisplayName("plain"))
	assert.Equal(t, "img", initStepDisplayName("host:5000/ns/img@sha256:abcdef"))
	// Degenerate ref — fall back to the raw image rather than empty.
	assert.Equal(t, "citeck/", initStepDisplayName("citeck/"))
}
