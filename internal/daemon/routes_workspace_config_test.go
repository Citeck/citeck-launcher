package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// wsConfigTestMux stands up a daemon over SQLite with an isolated CITECK_HOME
// (server mode) and a pristine workspace-v1.yml on disk for the default
// workspace, so the {id}/config routes resolve a real git baseline offline.
func wsConfigTestMux(t *testing.T, baseline string) (*Daemon, *http.ServeMux) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	// Priority-1 manual import: data/repo/workspace-v1.yml without .git.
	repoDir := filepath.Join(config.DataDir(), "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"), []byte(baseline), 0o644))

	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	d := testDaemon(t, store)
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

// wsCfgGet / wsCfgPut target the default workspace — the only one these tests
// seed on disk.
func wsCfgGet(t *testing.T, mux *http.ServeMux) api.WorkspaceConfigDto {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/default/config", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var dto api.WorkspaceConfigDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	return dto
}

func wsCfgPut(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/v1/workspaces/default/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestWorkspaceConfig_GetBaselineNoDelta(t *testing.T) {
	base := "quickStartVariants:\n  - name: Original\n"
	_, mux := wsConfigTestMux(t, base)
	dto := wsCfgGet(t, mux)
	assert.Equal(t, base, dto.Baseline)
	assert.Equal(t, base, dto.Content) // no delta → content == baseline
}

func TestWorkspaceConfig_PutStoresDeltaAndGetReflectsIt(t *testing.T) {
	base := "quickStartVariants:\n  - name: Original\n"
	d, mux := wsConfigTestMux(t, base)

	edited := "quickStartVariants:\n  - name: Edited\n"
	rec := wsCfgPut(t, mux, edited)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Delta persisted.
	blob, err := d.store.GetStateValue(wsConfigDeltaKey("default"))
	require.NoError(t, err)
	assert.NotEmpty(t, blob)

	// GET content reflects the edit; baseline stays the git reference.
	dto := wsCfgGet(t, mux)
	assert.Equal(t, base, dto.Baseline)
	assert.Contains(t, dto.Content, "Edited")
	assert.NotContains(t, dto.Content, "Original")
}

func TestWorkspaceConfig_PutEqualBaselineClearsDelta(t *testing.T) {
	base := "quickStartVariants:\n  - name: Original\n"
	d, mux := wsConfigTestMux(t, base)

	// Seed a delta first.
	require.Equal(t, http.StatusOK, wsCfgPut(t, mux, "quickStartVariants:\n  - name: Edited\n").Code)
	blob, _ := d.store.GetStateValue(wsConfigDeltaKey("default"))
	require.NotEmpty(t, blob)

	// Re-submit the exact baseline → delta cleared.
	require.Equal(t, http.StatusOK, wsCfgPut(t, mux, base).Code)
	blob, err := d.store.GetStateValue(wsConfigDeltaKey("default"))
	require.NoError(t, err)
	assert.Empty(t, blob)
}

func TestWorkspaceConfig_Reset(t *testing.T) {
	base := "quickStartVariants:\n  - name: Original\n"
	d, mux := wsConfigTestMux(t, base)
	require.Equal(t, http.StatusOK, wsCfgPut(t, mux, "quickStartVariants:\n  - name: Edited\n").Code)

	req := httptest.NewRequest("POST", "/api/v1/workspaces/default/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	blob, err := d.store.GetStateValue(wsConfigDeltaKey("default"))
	require.NoError(t, err)
	assert.Empty(t, blob)
	dto := wsCfgGet(t, mux)
	assert.Equal(t, base, dto.Content)
}

func TestWorkspaceConfig_PutInvalidYAML(t *testing.T) {
	_, mux := wsConfigTestMux(t, "quickStartVariants: []\n")
	rec := wsCfgPut(t, mux, "key: [unterminated\n  : : :")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWorkspaceConfig_UnknownWorkspace404(t *testing.T) {
	_, mux := wsConfigTestMux(t, "quickStartVariants: []\n")
	req := httptest.NewRequest("GET", "/api/v1/workspaces/nope/config", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
