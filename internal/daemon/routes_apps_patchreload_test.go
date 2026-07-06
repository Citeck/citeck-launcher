package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// newAppsTestDaemonRabbit stands up a Daemon whose active namespace carries a
// REAL namespace.Runtime (via SetStatusForTest — a real Start() would spawn
// the runtimeLoop and need a real Docker client, impractical in a unit test,
// the same constraint newReloadPlanTestDaemon documents for the reload-plan
// path) that knows a "rabbitmq" app via SetGeneratedDefs.
func newAppsTestDaemonRabbit(t *testing.T, status namespace.NsRuntimeStatus) (*http.ServeMux, *Daemon) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetGeneratedDefs([]appdef.ApplicationDef{{Name: "rabbitmq", Image: "rabbitmq:3"}})
	rt.SetStatusForTest(status)

	d := &Daemon{
		activeNs: &activeNamespace{
			runtime:   rt,
			nsConfig:  &namespace.Config{ID: "test"},
			bundleDef: &bundle.Def{Key: bundle.Key{Version: "1.0.0"}},
		},
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux, d
}

// A PUT config on a RUNNING namespace must route through the reload (so
// Generate re-runs and rewrites files), holding reloadMu while it does. Uses
// the reloadFn seam — content correctness lives in the namespace-level
// generator test (TestGenerate_EffectiveAndBaselineSplit).
func TestPutAppConfig_RunningRoutesThroughReload(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusRunning)
	reloadCalls := 0
	d.reloadFn = func() error {
		require.False(t, d.reloadMu.TryLock(), "reloadMu must be held while reloadFn runs")
		reloadCalls++
		return nil
	}

	body := "name: rabbitmq\nresources:\n  limits:\n    memory: 2g\n"
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/rabbitmq/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 1, reloadCalls, "running edit must trigger exactly one reload")
	require.Contains(t, rec.Body.String(), "updated and applied")
	require.True(t, d.reloadMu.TryLock(), "reloadMu must be released after the handler returns")
	d.reloadMu.Unlock()
}

// A PUT config on a STOPPED namespace must persist the patch WITHOUT routing
// through a reload — the edit applies on the next start. Pins the "no lock,
// no reload" half of the running/stopped branch added in Task 3.
func TestPutAppConfig_StoppedPersistsWithoutReload(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	reloadCalls := 0
	d.reloadFn = func() error {
		reloadCalls++
		return nil
	}

	body := "name: rabbitmq\nresources:\n  limits:\n    memory: 2g\n"
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/rabbitmq/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 0, reloadCalls, "stopped edit must not trigger a reload")
	require.Contains(t, rec.Body.String(), "applies on next start")
	require.True(t, d.reloadMu.TryLock(), "reloadMu must be free after a stopped-branch edit")
	d.reloadMu.Unlock()
	require.NotNil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "patch must still be persisted")
}

// A POST config/reset on a RUNNING namespace must also route through the
// reload, mirroring handlePutAppConfig, while preserving the deliberate
// nil-runtime→404 path (TestResetAppConfig_NoRuntimeReturnsNotFound).
func TestResetAppConfig_RunningRoutesThroughReload(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusRunning)
	require.NoError(t, d.activeNs.runtime.UpdateAppDef("rabbitmq",
		appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:3", ShmSize: "256m"}, true))
	require.NotNil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "precondition: a patch must be stored")

	reloadCalls := 0
	d.reloadFn = func() error {
		require.False(t, d.reloadMu.TryLock(), "reloadMu must be held while reloadFn runs")
		reloadCalls++
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/rabbitmq/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 1, reloadCalls, "running reset must trigger exactly one reload")
	require.True(t, d.reloadMu.TryLock(), "reloadMu must be released after the handler returns")
	d.reloadMu.Unlock()
	require.Nil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "patch must be cleared")
}

// A POST config/reset on a STOPPED namespace must clear the patch WITHOUT
// routing through a reload.
func TestResetAppConfig_StoppedPersistsWithoutReload(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	require.NoError(t, d.activeNs.runtime.UpdateAppDef("rabbitmq",
		appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:3", ShmSize: "256m"}, true))
	require.NotNil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "precondition: a patch must be stored")

	reloadCalls := 0
	d.reloadFn = func() error {
		reloadCalls++
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/rabbitmq/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 0, reloadCalls, "stopped reset must not trigger a reload")
	require.True(t, d.reloadMu.TryLock(), "reloadMu must be free after a stopped-branch reset")
	d.reloadMu.Unlock()
	require.Nil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "patch must be cleared")
}
