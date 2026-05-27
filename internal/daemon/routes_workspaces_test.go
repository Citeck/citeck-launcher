package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Helper: stand up a Daemon with SQLite store and routes mounted, then return
// an HTTP test server. The caller is responsible for setting/resetting the
// desktop-mode flag.
func newWorkspaceTestDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	d := &Daemon{store: store}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

// TestWorkspaceRoutes_ServerModeReturns404 verifies the mode gate: every
// workspace CRUD/activate route MUST respond 404 + DESKTOP_ONLY in server mode
// so server binaries don't expose half-implemented multi-workspace behavior.
func TestWorkspaceRoutes_ServerModeReturns404(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(config.ResetDesktopMode)

	_, mux := newWorkspaceTestDaemon(t)

	cases := []struct {
		method string
		path   string
	}{
		{"GET", api.Workspaces},
		{"POST", api.Workspaces},
		{"GET", "/api/v1/workspaces/foo"},
		{"PUT", "/api/v1/workspaces/foo"},
		{"DELETE", "/api/v1/workspaces/foo"},
		{"POST", "/api/v1/workspaces/foo/activate"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, http.NoBody)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			var body api.ErrorDto
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, api.ErrCodeDesktopOnly, body.Code)
		})
	}
}

// TestWorkspaceRoutes_DesktopCRUD walks the happy path: create → list → get →
// update → delete. Activate is exercised separately because the docker client
// init is not available under unit tests.
func TestWorkspaceRoutes_DesktopCRUD(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir()) // isolate WorkspacesDir() side effect of MkdirAll

	_, mux := newWorkspaceTestDaemon(t)

	// Create
	body, _ := json.Marshal(api.WorkspaceCreateDto{
		Name: "Acme Workspace", RepoURL: "https://example.test/repo.git",
	})
	req := httptest.NewRequest("POST", api.Workspaces, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "create body=%s", rec.Body.String())
	var created api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "acme-workspace", created.ID, "id slug derived from name")
	assert.Equal(t, "main", created.RepoBranch, "default branch applied when omitted")

	// Duplicate → 409
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", api.Workspaces, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)

	// List
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", api.Workspaces, http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.Equal(t, "acme-workspace", list[0].ID)

	// Get
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/workspaces/acme-workspace", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Get missing → 404 WORKSPACE_NOT_FOUND
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/workspaces/missing", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	var err404 api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &err404))
	assert.Equal(t, api.ErrCodeWorkspaceNotFound, err404.Code)

	// Update
	upd, _ := json.Marshal(api.WorkspaceUpdateDto{Name: "Renamed", RepoBranch: "develop"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/v1/workspaces/acme-workspace", bytes.NewReader(upd))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "update body=%s", rec.Body.String())
	var updated api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "Renamed", updated.Name)
	assert.Equal(t, "develop", updated.RepoBranch)
	assert.Equal(t, "https://example.test/repo.git", updated.RepoURL, "non-empty fields preserved on partial update")

	// Delete
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/api/v1/workspaces/acme-workspace", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Get after delete → 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/workspaces/acme-workspace", http.NoBody)
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestWorkspaceDelete_RefusesActive guards the constraint that the active
// workspace cannot be deleted — switching first is required to keep the
// daemon's docker client + runtime consistent.
func TestWorkspaceDelete_RefusesActive(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "current", Name: "Current", RepoURL: "https://example.test/x.git", RepoBranch: "main",
	}))
	d.workspaceID = "current"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/workspaces/current", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeWorkspaceInUse, body.Code)
}

// TestWorkspaceCreate_ValidationErrors covers the input-validation surface
// the handler enforces before storage is touched.
func TestWorkspaceCreate_ValidationErrors(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	_, mux := newWorkspaceTestDaemon(t)

	cases := []struct {
		name string
		body string
		want int
		msg  string
	}{
		{"missing name", `{"repoUrl":"https://example.test/x.git"}`, http.StatusBadRequest, "name is required"},
		{"missing repoUrl", `{"name":"foo"}`, http.StatusBadRequest, "repoUrl is required"},
		{"invalid json", `{not-json`, http.StatusBadRequest, "invalid request body"},
		{"id with slash", `{"id":"a/b","name":"X","repoUrl":"https://example.test/x.git"}`, http.StatusBadRequest, "invalid workspace id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", api.Workspaces, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)
			assert.Equal(t, c.want, rec.Code, "body=%s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), c.msg)
		})
	}
}
