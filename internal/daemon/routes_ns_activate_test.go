package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// TestNamespaceActivate_DesktopOnly mirrors the workspace activate route's
// mode gate: server-mode binaries must respond 404 + DESKTOP_ONLY when the
// caller hits /api/v1/namespaces/{id}/activate (multi-namespace switching
// is a desktop-mode-only feature).
func TestNamespaceActivate_DesktopOnly(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(config.ResetDesktopMode)

	_, mux := newWorkspaceTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/v1/namespaces/foo/activate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeDesktopOnly, body.Code)
}

// TestNamespaceActivate_RejectsInvalidID guards the validateID call at the
// top of handleActivateNamespace. The chosen ID ("bad@name") passes through
// ServeMux's routing untouched (no normalisation kicks in for "@") but
// fails the safeIDPattern check inside validateID — so a mutation that
// removes the validateID gate falls through to the on-disk lookup branch
// and returns 404 NAMESPACE_NOT_FOUND instead of 400. Asserting both the
// status code and the absence of the NAMESPACE_NOT_FOUND error code rules
// out the false-positive shape the Opus review flagged.
func TestNamespaceActivate_RejectsInvalidID(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	_, mux := newWorkspaceTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/v1/namespaces/bad@name/activate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"validateID must reject an ID outside the safeIDPattern; body=%s", rec.Body.String())

	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEqual(t, api.ErrCodeNamespaceNotFound, body.Code,
		"a NAMESPACE_NOT_FOUND code here would mean validateID was bypassed and the os.Stat branch ran")
}

// TestNamespaceActivate_NotFound exercises the explicit
// NAMESPACE_NOT_FOUND branch: a valid ID that has no namespace.yml on disk
// returns 404 + ErrCodeNamespaceNotFound (not the misleading APP_NOT_FOUND
// that was used before the review fix).
func TestNamespaceActivate_NotFound(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	d.workspaceID = "default"
	// Different ID than the active one — bypasses the "already active" short-
	// circuit and falls through to the os.Stat 404 branch.

	req := httptest.NewRequest("POST", "/api/v1/namespaces/ghost-ns/activate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())

	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeNamespaceNotFound, body.Code,
		"reviewer flagged: response should not say 'app not found' for a missing namespace")
}

// TestNamespaceDeactivate_DesktopOnly: same gate on the sibling deactivate route.
func TestNamespaceDeactivate_DesktopOnly(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(config.ResetDesktopMode)

	_, mux := newWorkspaceTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/v1/namespaces/deactivate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeDesktopOnly, body.Code)
}
