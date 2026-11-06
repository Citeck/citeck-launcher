package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
)

func configureSnapshotFloor(d *Daemon) {
	d.activeNs.nsConfig.BundleRef = bundle.Ref{Repo: "community", Key: "2026.2"}
	d.activeNs.workspaceConfig = &bundle.WorkspaceConfig{Snapshots: []bundle.SnapshotDef{
		{ID: "demo", Name: "New demo", URL: "https://example.invalid/demo.zip", MinBundleVersion: "2026.3"},
		{ID: "legacy", Name: "Legacy demo"},
	}}
}

func TestWorkspaceSnapshotAvailabilityUsesSelectedBundle(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)
	configureSnapshotFloor(d)
	for _, key := range []string{"2026.2", "2026.3", "2026.10"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", api.WorkspaceSnapshots+"?bundleKey="+key, http.NoBody)
		req.Header.Set("X-Citeck-Locale", "ru")
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var rows []struct {
			MinBundleVersion  string `json:"minBundleVersion"`
			UnavailableReason string `json:"unavailableReason"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		require.Len(t, rows, 2)
		require.Equal(t, "2026.3", rows[0].MinBundleVersion)
		if key == "2026.2" {
			require.Contains(t, rows[0].UnavailableReason, "требует бандл версии 2026.3")
		} else {
			require.Empty(t, rows[0].UnavailableReason)
		}
		require.Empty(t, rows[1].UnavailableReason, "unbounded snapshots retain their old behavior")
	}
}

func TestSnapshotVersionRefusalBeforeDownloadAndRestore(t *testing.T) {
	d, mux, dir := newSnapshotsTestDaemon(t)
	configureSnapshotFloor(d)
	file := filepath.Join(dir, "demo.zip")
	require.NoError(t, os.WriteFile(file, []byte("unchanged cached bytes"), 0o600))
	for _, route := range []struct{ path, body string }{
		{api.SnapshotsDownload, `{"name":"demo","url":"https://example.invalid/demo.zip"}`},
		{api.SnapshotsImport + "?name=demo.zip", ""},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", route.path, bytes.NewBufferString(route.body)))
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), api.ErrCodeSnapshotBundleIncompatible)
		require.Contains(t, rec.Body.String(), "requires bundle 2026.3")
	}
	// CLI reads this refusal before offering to stop a running namespace.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", api.Snapshots, http.NoBody))
	var rows []api.SnapshotDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].UnavailableReason, "requires bundle 2026.3")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "unchanged cached bytes", string(raw))
	require.Empty(t, d.snapshotOps, "refusal starts no background download/import operation")
}

func TestCreateSnapshotMinimumCheckedBeforePersistenceIncludingTemplate(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "wsTarget", map[string]string{"2026.2": "", "2026.3": ""})
	wsFile := filepath.Join(config.WorkspaceRepoDir("wsTarget"), "workspace-v1.yml")
	raw, err := os.ReadFile(wsFile)
	require.NoError(t, err)
	raw = append(raw, []byte("snapshots:\n  - id: demo\n    minBundleVersion: 2026.3\nnamespaceTemplates:\n  - id: with-data\n    config:\n      snapshot: demo\n")...)
	require.NoError(t, os.WriteFile(wsFile, raw, 0o600))
	// Deliberately different active workspace: target definitions must win.
	d.activeNs.workspaceConfig = &bundle.WorkspaceConfig{
		Snapshots:          []bundle.SnapshotDef{{ID: "demo"}},
		NamespaceTemplates: []bundle.NamespaceTemplate{{ID: "with-data", Config: map[string]any{"snapshot": "demo"}}},
	}
	for _, fromTemplate := range []bool{false, true} {
		req := api.NamespaceCreateDto{Name: "New stand", AuthType: "KEYCLOAK", BundleRepo: "community", BundleKey: "2026.2", WorkspaceID: "wsTarget"}
		if fromTemplate {
			req.Template = "with-data"
		} else {
			req.Snapshot = "demo"
		}
		body, marshalErr := json.Marshal(req)
		require.NoError(t, marshalErr)
		rec := httptest.NewRecorder()
		request := httptest.NewRequest("POST", api.Namespaces, bytes.NewReader(body))
		request.Header.Set("X-Citeck-Locale", "ru")
		mux.ServeHTTP(rec, request)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), api.ErrCodeSnapshotBundleIncompatible)
		require.Contains(t, rec.Body.String(), "требует бандл версии 2026.3")
		rows, listErr := d.store.ListNamespaces("wsTarget")
		require.NoError(t, listErr)
		require.Empty(t, rows)
		// The same template/snapshot IS usable at the inclusive boundary,
		// checked through the production config builder without touching Docker.
		req.BundleKey = "2026.3"
		cfg, buildErr := d.buildNamespaceConfigFromCreate(req, "wsTarget")
		require.NoError(t, buildErr)
		require.Equal(t, "demo", cfg.Snapshot)
	}
	_, cacheErr := os.Stat(config.WorkspaceSnapshotsDir("wsTarget"))
	require.True(t, os.IsNotExist(cacheErr))
}

func TestCreateTimeSnapshotRechecksFloorBeforeCache(t *testing.T) {
	d, _, _ := newSnapshotsTestDaemon(t)
	configureSnapshotFloor(d)
	err := d.downloadAndImportSnapshot(nil, "demo", "ws-test", "new-ns", "2026.2")
	require.ErrorContains(t, err, "requires bundle 2026.3")
	_, statErr := os.Stat(config.WorkspaceSnapshotsDir("ws-test"))
	require.True(t, os.IsNotExist(statErr))
}
