package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
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

// TestGenerateLinksCustomDependsOn pins the workspace-config custom-link gating:
// no deps ⇒ enabled; a RUNNING dep ⇒ enabled; a present-but-not-running dep ⇒
// disabled; a dep absent from the namespace ⇒ link hidden (omitted).
func TestGenerateLinksCustomDependsOn(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())

	// "stopped" is configured (in generatedDefs) but not in r.apps (never
	// started); "uiserv"/"eproc" are live with distinct statuses.
	r.SetGeneratedDefs([]appdef.ApplicationDef{
		{Name: "uiserv"}, {Name: "eproc"}, {Name: "stopped"},
	})
	r.SetCustomLinks([]bundle.WorkspaceLink{
		{Name: "NoDeps", URL: "http://x/nodeps"},
		{Name: "OnRunning", URL: "http://x/run", DependsOn: []string{"uiserv"}},
		{Name: "OnStarting", URL: "http://x/starting", DependsOn: []string{"eproc"}},
		{Name: "OnStopped", URL: "http://x/stopped", DependsOn: []string{"stopped"}},
		{Name: "OnGhost", URL: "http://x/ghost", DependsOn: []string{"ghost"}},
	})
	r.mu.Lock()
	r.apps["uiserv"] = &AppRuntime{Name: "uiserv", Status: AppStatusRunning}
	r.apps["eproc"] = &AppRuntime{Name: "eproc", Status: AppStatusStarting}
	r.mu.Unlock()

	byName := map[string]api.LinkDto{}
	for _, l := range r.ToNamespaceDto().Links {
		byName[l.Name] = l
	}

	if l, ok := byName["NoDeps"]; !ok || l.Disabled || !l.Custom {
		t.Errorf("NoDeps: ok=%v disabled=%v custom=%v", ok, l.Disabled, l.Custom)
	}
	if l, ok := byName["OnRunning"]; !ok || l.Disabled {
		t.Errorf("OnRunning should be enabled: ok=%v disabled=%v", ok, l.Disabled)
	}
	// Custom links are pinned below every built-in (max built-in order is 101).
	if l := byName["NoDeps"]; l.Order <= 101 {
		t.Errorf("custom link order %v should be pinned to the bottom (>101)", l.Order)
	}
	if l, ok := byName["OnStarting"]; !ok || !l.Disabled {
		t.Errorf("OnStarting should be disabled (dep not RUNNING): ok=%v disabled=%v", ok, l.Disabled)
	}
	if l, ok := byName["OnStopped"]; !ok || !l.Disabled {
		t.Errorf("OnStopped should be disabled (configured, not running): ok=%v disabled=%v", ok, l.Disabled)
	}
	if _, ok := byName["OnGhost"]; ok {
		t.Error("OnGhost should be hidden (dep absent from namespace)")
	}
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
