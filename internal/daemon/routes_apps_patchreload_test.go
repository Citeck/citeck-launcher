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

	body := "name: rabbitmq\nimage: rabbitmq:3\nresources:\n  limits:\n    memory: 2g\n"
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

	body := "name: rabbitmq\nimage: rabbitmq:3\nresources:\n  limits:\n    memory: 2g\n"
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

// A port the container could not be created with is refused at the editor —
// nothing persisted, nothing reloaded — instead of failing at the container's
// start with the app down. The address-bound form is accepted: that is the
// safe way to reach a server's database, on this machine only.
func TestPutAppConfig_PortsAreValidatedBeforeAnythingIsSaved(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusRunning)
	reloadCalls := 0
	d.reloadFn = func() error {
		reloadCalls++
		return nil
	}
	put := func(ports string) *httptest.ResponseRecorder {
		body := "name: rabbitmq\nimage: rabbitmq:3\nports:\n  - \"" + ports + "\"\n"
		req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/rabbitmq/config", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	for _, bad := range []string{"localhost:15672:15672", "15672:x", "15672", "1:1/foo"} {
		rec := put(bad)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: body=%s", bad, rec.Body.String())
		require.Contains(t, rec.Body.String(), "invalid ports", bad)
	}
	require.Nil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "a refused edit persists nothing")
	require.Equal(t, 0, reloadCalls, "a refused edit reloads nothing")

	rec := put("127.0.0.1:15672:15672")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 1, reloadCalls)
}

// An edit body over the limit is refused, not truncated: a truncated YAML can
// still parse, and on a live stand a 543 KB edit cut at 512 KiB left only its
// comment lines, decoded as an empty def and saved — postgres lost its image,
// cmd, env and volumes. An edit without an image is refused for the same
// outcome by the shorter road.
func TestPutAppConfig_AnOversizedOrImagelessEditIsRefused(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusRunning)
	reloadCalls := 0
	d.reloadFn = func() error {
		reloadCalls++
		return nil
	}
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/rabbitmq/config", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	padding := strings.Repeat("# the error banner of an earlier attempt\n", 512*1024/40+1)
	rec := put(padding + "---\nname: rabbitmq\nimage: rabbitmq:3\n")
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())

	rec = put("# only comments survived\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "image is required")

	require.Nil(t, d.activeNs.runtime.AppPatch("rabbitmq"), "nothing persisted")
	require.Equal(t, 0, reloadCalls, "nothing reloaded")
}

// A bare container port the app ALREADY carries (a workspace additionalApps
// entry, ignored by the assembly as it always was) must not make every edit
// of that app fail; one the operator writes is still refused.
func TestPutAppConfig_ABareContainerPortTheAppAlreadyCarriesIsTolerated(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.runtime.SetGeneratedDefs([]appdef.ApplicationDef{{Name: "rabbitmq", Image: "rabbitmq:3", Ports: []string{"15672:15672", "8025"}}})
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/rabbitmq/config", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	rec := put("name: rabbitmq\nimage: rabbitmq:3\nports: [\"15672:15672\", \"8025\"]\nshmSize: 256m\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = put("name: rabbitmq\nimage: rabbitmq:3\nports: [\"15672:15672\", \"8025\", \"9000\"]\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "9000")
}
