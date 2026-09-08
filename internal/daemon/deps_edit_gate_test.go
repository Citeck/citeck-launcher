package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// newEditGateDaemon stands up a Daemon whose active namespace knows a pinned
// dependency (postgres, on 17.5) and one ordinary app. The namespace is
// STOPPED — the default for a fresh Runtime — so an accepted edit persists the
// patch without routing through a reload.
func newEditGateDaemon(t *testing.T, pins map[deps.ID]deps.DependencyState) (*Daemon, *http.ServeMux, *namespace.Runtime) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetGeneratedDefs([]appdef.ApplicationDef{
		{Name: "postgres", Image: "postgres:17.5"},
		{Name: "gateway", Image: "gw:1"},
	})
	rt.RestoreDependencyState(pins, nil, nil)
	d := &Daemon{activeNs: &activeNamespace{runtime: rt, nsConfig: &namespace.Config{ID: "ns1"}}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, rt
}

func newPinnedEditGateDaemon(t *testing.T) (*Daemon, *http.ServeMux, *namespace.Runtime) {
	t.Helper()
	return newEditGateDaemon(t, map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}})
}

func putAppConfig(mux *http.ServeMux, app, yamlBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+app+"/config", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestBreakingImageEditOnADependencyIsRefused(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.Contains(t, rec.Body.String(), "citeck deps upgrade postgres")
	assert.Nil(t, rt.AppPatch("postgres"), "a refused edit must not be persisted")
}

func TestNonBreakingImageEditOnADependencyIsAccepted(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.11\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}

func TestImageEditOnANonDependencyIsNotGated(t *testing.T) {
	_, mux, _ := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "gateway", "name: gateway\nimage: gw:2\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// With no pin there is no recorded version the data runs on, so there is
// nothing to refuse against: the gate must stay out of the way (this is the
// pre-pin namespace, before the first start seeds a pin).
func TestBreakingImageEditWithoutAPinIsNotGated(t *testing.T) {
	_, mux, rt := newEditGateDaemon(t, nil)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}

// An edit that leaves the image out entirely (a memory-limit tweak, the common
// case) says nothing about the version and must pass even on a pinned
// dependency — the generator's own pin gate fills the image in.
func TestNonImageEditOnAPinnedDependencyIsNotGated(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nresources:\n  limits:\n    memory: 2g\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}
