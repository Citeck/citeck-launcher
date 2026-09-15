package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The indicator's data rides the namespace DTO, like dependencyUpgrades: it is
// the daemon's knowledge (activeNamespace), not the runtime's.
func TestGetNamespace_CarriesTheNewerBundle(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = &bundle.NewerBundle{Version: "2026.3"}

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.NewerBundle)
	assert.Equal(t, "2026.3", dto.NewerBundle.Version)
	assert.Empty(t, dto.NewerBundle.RequiresLauncher,
		"empty means the operator can switch to it right now")
}

func TestGetNamespace_CarriesTheLauncherFloorOfTheNewerBundle(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = &bundle.NewerBundle{Version: "2026.3", RequiresLauncher: "2.13.0"}

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.NewerBundle)
	assert.Equal(t, "2.13.0", dto.NewerBundle.RequiresLauncher)
}

// Absent, not present-and-empty: the client branches on the field existing.
func TestGetNamespace_NoNewerBundleIsAbsentFromTheBody(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = nil

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.NotContains(t, rec.Body.String(), "newerBundle")
}
