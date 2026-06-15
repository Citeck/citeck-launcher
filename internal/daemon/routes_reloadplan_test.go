package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// planStubDocker implements docker.RuntimeClient by embedding the (nil)
// interface — any method PlanRegenerate is NOT supposed to touch panics
// loudly, which doubles as a "the plan stays side-effect-free" guard.
// GetImageDigest is the single read-only call the plan path performs.
type planStubDocker struct {
	docker.RuntimeClient
}

func (planStubDocker) GetImageDigest(_ context.Context, img string) string {
	return "sha256:stub-" + img
}

// newReloadPlanTestDaemon stands up a Daemon whose active namespace carries a
// REAL namespace.Runtime (seeded via InjectAppsForTest) and a seamed
// resolve+generate phase, so the handler test exercises the genuine verdict
// classification end-to-end without git/bundle/generator I/O.
func newReloadPlanTestDaemon(t *testing.T, current []*namespace.AppRuntime,
	detached map[string]bool, inputs *reloadPlanInputs) *http.ServeMux {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.InjectAppsForTest(current...)
	if detached != nil {
		rt.SetManualStoppedApps(detached)
	}

	d := &Daemon{
		activeNs: &activeNamespace{
			runtime:   rt,
			nsConfig:  &namespace.Config{ID: "test"},
			bundleDef: &bundle.Def{Key: bundle.Key{Version: "1.0.0"}},
		},
		planInputsFn: func(activeNamespace) (*reloadPlanInputs, error) { return inputs, nil },
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux
}

// stubAppDef builds a definition with a fixed digest so the stub docker is
// not consulted for it.
func stubAppDef(name, image string, env map[string]string) appdef.ApplicationDef {
	return appdef.ApplicationDef{Name: name, Image: image, ImageDigest: "sha256:stub-" + image, Environments: env}
}

func getReloadPlan(t *testing.T, mux *http.ServeMux) (*httptest.ResponseRecorder, api.ReloadPlanDto) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.NamespaceReloadPlan, http.NoBody))
	var dto api.ReloadPlanDto
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto), "body=%s", rec.Body.String())
	}
	return rec, dto
}

func planAppByName(t *testing.T, dto api.ReloadPlanDto, name string) api.ReloadPlanAppDto {
	t.Helper()
	for _, a := range dto.Apps {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no plan app %q in %+v", name, dto.Apps)
	return api.ReloadPlanAppDto{}
}

func TestReloadPlan_VerdictClassification(t *testing.T) {
	keepDef := stubAppDef("keep-app", "img-keep:1", nil)
	recreateOld := stubAppDef("recreate-app", "img-rec:1", map[string]string{"OLD": "x"})
	recreateNew := stubAppDef("recreate-app", "img-rec:2", map[string]string{"NEW": "y"})
	detachedDef := stubAppDef("detached-app", "img-det:1", nil)
	createDef := stubAppDef("create-app", "img-new:1", nil)
	removeDef := stubAppDef("remove-app", "img-rem:1", nil)

	mux := newReloadPlanTestDaemon(t,
		[]*namespace.AppRuntime{
			{Name: "keep-app", Status: namespace.AppStatusRunning, Def: keepDef},
			{Name: "recreate-app", Status: namespace.AppStatusRunning, Def: recreateOld},
			{Name: "detached-app", Status: namespace.AppStatusStopped, Def: detachedDef},
			{Name: "remove-app", Status: namespace.AppStatusRunning, Def: removeDef},
		},
		map[string]bool{"detached-app": true},
		&reloadPlanInputs{
			apps:         []appdef.ApplicationDef{keepDef, recreateNew, detachedDef, createDef},
			bundleBefore: "1.0.0",
			bundleAfter:  "1.1.0",
		})

	rec, dto := getReloadPlan(t, mux)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	assert.Equal(t, "keep", planAppByName(t, dto, "keep-app").Verdict)
	assert.Equal(t, "create", planAppByName(t, dto, "create-app").Verdict)
	assert.Equal(t, "detached", planAppByName(t, dto, "detached-app").Verdict)
	assert.Equal(t, "remove", planAppByName(t, dto, "remove-app").Verdict)

	rec2 := planAppByName(t, dto, "recreate-app")
	assert.Equal(t, "recreate", rec2.Verdict)
	assert.Contains(t, rec2.DiffAdded, "env:NEW=y")
	assert.Contains(t, rec2.DiffRemoved, "env:OLD=x")

	assert.Equal(t, api.ReloadPlanSummaryDto{Create: 1, Recreate: 1, Keep: 1, Remove: 1, Detached: 1}, dto.Summary)
	assert.Equal(t, "1.0.0", dto.BundleBefore)
	assert.Equal(t, "1.1.0", dto.BundleAfter)
	assert.False(t, dto.WouldSkip)
}

func TestReloadPlan_WouldSkipOnSmallerFallbackSet(t *testing.T) {
	// Cached-bundle fallback with fewer apps than currently running: a real
	// reload preserves the runtime (doReloadEx guard) — the plan must say so.
	a := stubAppDef("a", "img-a:1", nil)
	b := stubAppDef("b", "img-b:1", nil)
	mux := newReloadPlanTestDaemon(t,
		[]*namespace.AppRuntime{
			{Name: "a", Status: namespace.AppStatusRunning, Def: a},
			{Name: "b", Status: namespace.AppStatusRunning, Def: b},
		},
		nil,
		&reloadPlanInputs{apps: []appdef.ApplicationDef{a}, bundleFallback: true})

	rec, dto := getReloadPlan(t, mux)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, dto.WouldSkip)
	assert.True(t, dto.BundleFallback)
	assert.Equal(t, "remove", planAppByName(t, dto, "b").Verdict)
}

func TestReloadPlan_NotConfigured(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.NamespaceReloadPlan, http.NoBody))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeNotConfigured)
}

func TestReloadPlan_ConflictWhileReloadInProgress(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.NamespaceReloadPlan, http.NoBody))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeReloadInProgress)
}
