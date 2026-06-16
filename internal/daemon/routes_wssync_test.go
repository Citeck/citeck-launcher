package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// --- Welcome-data 502 gate (no silent fallback, Kotlin 1.x parity) ---

const testWsSyncErrMsg = "sync workspace repo https://gitlab.example.com/citeck/ws.git: " +
	"clone or pull /tmp/x: git clone https://gitlab.example.com/citeck/ws.git: authentication required"

// newWsSyncErrMux mounts the routes on a daemon whose active snapshot carries
// a recorded workspace-repo sync failure (custom repo, auth error, no cache —
// the state loadNamespace records via resolver.WorkspaceSyncError).
func newWsSyncErrMux(t *testing.T, wsSyncError string) *http.ServeMux {
	t.Helper()
	d := &Daemon{activeNs: &activeNamespace{wsSyncError: wsSyncError}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux
}

func getJSON(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestQuickStarts_WsRepoSyncFailedReturns502: the quick-starts handler must
// surface a broken custom workspace repo as 502 WS_REPO_SYNC_FAILED instead of
// silently serving the built-in fallback quick start (which led users into an
// infra-only namespace).
func TestQuickStarts_WsRepoSyncFailedReturns502(t *testing.T) {
	mux := newWsSyncErrMux(t, testWsSyncErrMsg)

	rec := getJSON(t, mux, api.QuickStarts)
	require.Equal(t, http.StatusBadGateway, rec.Code, "body=%s", rec.Body.String())

	var errResp api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Equal(t, api.ErrCodeWsRepoSyncFailed, errResp.Code)
	// The web GitPullErrorDialog heuristic matches on the git wording; the
	// message must carry the repo URL and the underlying git error text.
	assert.Contains(t, errResp.Message, "https://gitlab.example.com/citeck/ws.git")
	assert.Contains(t, errResp.Message, "authentication required")
}

// TestQuickStarts_HealthyWorkspaceStays200: without a recorded sync error the
// handler keeps its historical contract (200 + [] for an empty workspace).
func TestQuickStarts_HealthyWorkspaceStays200(t *testing.T) {
	mux := newWsSyncErrMux(t, "")

	rec := getJSON(t, mux, api.QuickStarts)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}

// TestWorkspaceSnapshots_WsRepoSyncFailedReturns502: the other Welcome-data
// surface shares the same gate — a broken workspace must not masquerade as a
// workspace with no snapshots.
func TestWorkspaceSnapshots_WsRepoSyncFailedReturns502(t *testing.T) {
	mux := newWsSyncErrMux(t, testWsSyncErrMsg)

	rec := getJSON(t, mux, api.WorkspaceSnapshots)
	require.Equal(t, http.StatusBadGateway, rec.Code)

	var errResp api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Equal(t, api.ErrCodeWsRepoSyncFailed, errResp.Code)

	// Healthy path unchanged.
	okRec := getJSON(t, newWsSyncErrMux(t, ""), api.WorkspaceSnapshots)
	require.Equal(t, http.StatusOK, okRec.Code)
	assert.JSONEq(t, "[]", okRec.Body.String())
}

// --- Read-path self-heal: a cached auth error clears once the token is added ---

// TestQuickStarts_SelfHealClearsCachedAuthError: a workspace activated before
// its git token existed carries a cached wsSyncError. Once the token is added,
// the read-path self-heal (activeWorkspaceConfigForRead → re-resolve) must
// re-run the resolution against the current store, clear the error, and serve
// 200 — no daemon restart. The wsCfgResolveFn seam stands in for the real git
// clone the production path performs.
func TestQuickStarts_SelfHealClearsCachedAuthError(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "ws-x", Name: "Customer", RepoURL: "https://gitlab.example.com/citeck/ws.git",
		RepoBranch: "main", AuthType: "TOKEN",
	}))
	d.activeNs.workspaceID = "ws-x"
	d.activeNs.wsSyncError = testWsSyncErrMsg // cached at activation (token absent)

	// Token added since → the re-resolve now succeeds.
	resolved := &bundle.WorkspaceConfig{
		QuickStartVariants: []bundle.QuickStartVariant{{Name: "Community"}},
	}
	calls := 0
	d.wsCfgResolveFn = func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		calls++
		assert.Equal(t, "ws-x", ws.ID, "seam must receive the ACTIVE workspace")
		return resolved, nil
	}

	rec := getJSON(t, mux, api.QuickStarts)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var qs []api.QuickStartDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &qs))
	require.Len(t, qs, 1)
	assert.Equal(t, "Community", qs[0].Name)
	assert.Empty(t, d.active().wsSyncError, "cached error must clear after a successful re-resolve")

	// A second read is served from the now-clean cache without re-resolving.
	rec2 := getJSON(t, mux, api.QuickStarts)
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, 1, calls, "second read must not re-resolve once the error cleared")
}

// TestQuickStarts_SelfHealStillFailingStays502: if the re-resolve still fails
// (token still wrong/missing), the cached error is refreshed to the latest
// failure and the surface keeps returning 502 — no silent fallback.
func TestQuickStarts_SelfHealStillFailingStays502(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "ws-x", Name: "Customer", RepoURL: "https://gitlab.example.com/citeck/ws.git",
		RepoBranch: "main", AuthType: "TOKEN",
	}))
	d.activeNs.workspaceID = "ws-x"
	d.activeNs.wsSyncError = "stale activation-time error"
	d.wsCfgResolveFn = func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		return nil, errors.New(testWsSyncErrMsg)
	}

	rec := getJSON(t, mux, api.QuickStarts)
	require.Equal(t, http.StatusBadGateway, rec.Code, "body=%s", rec.Body.String())

	var errResp api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Equal(t, api.ErrCodeWsRepoSyncFailed, errResp.Code)
	assert.Contains(t, errResp.Message, "authentication required")
	assert.Equal(t, testWsSyncErrMsg, d.active().wsSyncError, "cached error refreshed to the latest failure")
}

// --- SwitchWorkspace: custom repo that can't sync fails the switch ---

// TestWorkspaceActivate_RepoSyncFailedReturns502: switching TO a workspace
// whose custom repo can't sync (auth error, no cache) must fail with 502
// WS_REPO_SYNC_FAILED and leave the daemon on the previous workspace (1.x
// parity: workspace selection failed hard). Uses the wsCfgResolveFn seam —
// the production path performs a real git clone.
func TestWorkspaceActivate_RepoSyncFailedReturns502(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "ws-broken", Name: "Broken", RepoURL: "https://gitlab.example.com/citeck/ws.git",
		RepoBranch: "main", AuthType: "TOKEN",
	}))
	d.activeNs.workspaceID = "default"
	d.wsCfgResolveFn = func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		return nil, errors.New(testWsSyncErrMsg)
	}

	req := httptest.NewRequest("POST", "/api/v1/workspaces/ws-broken/activate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code, "body=%s", rec.Body.String())

	var errResp api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Equal(t, api.ErrCodeWsRepoSyncFailed, errResp.Code)
	assert.Contains(t, errResp.Message, "authentication required")
	assert.Contains(t, errResp.Message, "https://gitlab.example.com/citeck/ws.git")

	// The failed switch must not have committed anything.
	assert.Equal(t, "default", d.activeWorkspaceID(), "daemon must stay on the previous workspace")
	state, err := d.store.GetState()
	require.NoError(t, err)
	assert.NotEqual(t, "ws-broken", state.WorkspaceID, "selection must not be persisted on a failed switch")
}

// TestWorkspaceActivate_SuccessInstallsWorkspaceConfig: a successful strict
// resolve commits the switch AND populates the active workspaceConfig so the
// Welcome surfaces (quick starts) work right after switching — even before any
// namespace exists in the target workspace.
func TestWorkspaceActivate_SuccessInstallsWorkspaceConfig(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())

	d, mux := newWorkspaceTestDaemon(t)
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{
		ID: "ws-good", Name: "Good", RepoURL: "https://gitlab.example.com/citeck/ws.git",
		RepoBranch: "main",
	}))
	d.activeNs.workspaceID = "default"
	resolved := &bundle.WorkspaceConfig{
		QuickStartVariants: []bundle.QuickStartVariant{{Name: "Community"}},
	}
	d.wsCfgResolveFn = func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error) {
		assert.Equal(t, "ws-good", ws.ID, "seam must receive the TARGET workspace")
		return resolved, nil
	}

	req := httptest.NewRequest("POST", "/api/v1/workspaces/ws-good/activate", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	act := d.active()
	assert.Equal(t, "ws-good", act.workspaceID)
	assert.Same(t, resolved, act.workspaceConfig, "resolved config must be installed for the Welcome surfaces")
	assert.Empty(t, act.wsSyncError)
}
