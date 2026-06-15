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
	d := &Daemon{store: store, activeNs: &activeNamespace{}}
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
	// ID is an opaque server-generated slug (Kotlin IdUtils.createStrId
	// parity), not derived from the human Name. We just assert it's a
	// non-empty lowercase base32-ish token.
	assert.NotEmpty(t, created.ID, "id slug generated server-side")
	assert.NotEqual(t, "acme-workspace", created.ID, "id is generated, not slugified name")
	createdID := created.ID
	assert.Equal(t, "main", created.RepoBranch, "default branch applied when omitted")

	// A second POST with the same name does NOT collide (random ID each time).
	// The dedup semantic is per-ID, not per-name — names are reference info.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", api.Workspaces, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var second api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &second))
	assert.NotEqual(t, createdID, second.ID, "each create gets a fresh ID")

	// Get the first workspace by its generated ID.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/workspaces/"+createdID, http.NoBody)
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
	req = httptest.NewRequest("PUT", "/api/v1/workspaces/"+createdID, bytes.NewReader(upd))
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
	req = httptest.NewRequest("DELETE", "/api/v1/workspaces/"+createdID, http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Get after delete → 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/workspaces/"+createdID, http.NoBody)
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
	d.activeNs.workspaceID = "current"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/workspaces/current", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeWorkspaceInUse, body.Code)
}

// TestWorkspaceSecretID_CRUDContract pins the secretId wire contract the web
// dialog codes against: create stores + echoes it, GET returns it, update with
// the field ABSENT leaves it unchanged, update with "" unlinks it.
func TestWorkspaceSecretID_CRUDContract(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	_, mux := newWorkspaceTestDaemon(t)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var rdr = http.NoBody
		req := httptest.NewRequest(method, path, rdr)
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Create with secretId.
	rec := do("POST", api.Workspaces,
		`{"name":"Acme","repoUrl":"https://gitlab.example.com/x.git","authType":"TOKEN","secretId":"shared-token"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var created api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "shared-token", created.SecretID, "create must echo the linked secret")

	// GET returns it (the edit dialog preselects the current link).
	rec = do("GET", "/api/v1/workspaces/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "shared-token", got.SecretID)

	// Update with secretId ABSENT → unchanged (pointer sentinel).
	rec = do("PUT", "/api/v1/workspaces/"+created.ID, `{"name":"Renamed"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var updated api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "shared-token", updated.SecretID, "absent secretId must keep the link")

	// Update with secretId "" → unlink.
	rec = do("PUT", "/api/v1/workspaces/"+created.ID, `{"secretId":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var unlinked api.WorkspaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &unlinked))
	assert.Empty(t, unlinked.SecretID, `"" must unlink the secret reference`)

	// Invalid secretId rejected.
	rec = do("PUT", "/api/v1/workspaces/"+created.ID, `{"secretId":"../etc/passwd"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestWorkspaceDelete_NeverDeletesSharedSecret pins the reusable-secrets
// invariant: deleting a workspace must NOT delete the secret it references via
// SecretID — other workspaces may share the same token. (Workspace delete has
// never deleted secrets, including the legacy ws:{id}:repo one; this test
// keeps it that way now that secrets are explicitly shared.)
func TestWorkspaceDelete_NeverDeletesSharedSecret(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	svc, err := storage.NewSecretService(d.store)
	require.NoError(t, err)
	d.secretService = svc
	require.NoError(t, svc.SetMasterPassword("test-master", false))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "shared-gitlab-token", Name: "GitLab", Type: storage.SecretGitToken},
		Value:      "glpat-shared",
	}))
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "ws-del", Name: "Doomed", RepoURL: "https://gitlab.example.com/x.git",
		AuthType: "TOKEN", SecretID: "shared-gitlab-token",
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/workspaces/ws-del", http.NoBody)
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	got, err := svc.GetSecret("shared-gitlab-token")
	require.NoError(t, err, "shared secret must survive workspace delete")
	assert.Equal(t, "glpat-shared", got.Value)
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
