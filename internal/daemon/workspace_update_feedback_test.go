package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestExplicitWorkspaceUpdateReportsAuthFailureWithUsableCache(t *testing.T) {
	for _, action := range []string{"workspace", "start", "force-start"} {
		t.Run(action, func(t *testing.T) {
			config.SetDesktopMode(true)
			t.Cleanup(config.ResetDesktopMode)
			t.Setenv("CITECK_HOME", t.TempDir())
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
			t.Cleanup(srv.Close)
			store, err := storage.NewSQLiteStore(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			d := newUpdateStartTestDaemon(t, "ns", namespace.NsStatusStopped)
			d.store = store
			d.secretService, err = storage.NewSecretService(store)
			require.NoError(t, err)
			d.activeNs.workspaceID = "private"
			d.activeNs.workspaceConfig = &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{ID: "local"}}}
			require.NoError(t, store.SaveWorkspace(storage.WorkspaceDto{ID: "private", RepoURL: srv.URL + "/workspace.git"}))
			require.NoError(t, store.SaveNamespaceConfig("private", "ns", "Test", "id: ns\nname: Test\nbundleRef: local:1.0\n"))
			cached := filepath.Join(config.BundlesDataDir("private"), "bundles", "workspace")
			require.NoError(t, os.MkdirAll(filepath.Join(cached, "releases"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(cached, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: local\n    path: releases\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(cached, "releases", "1.0.yml"), []byte("{}\n"), 0o600))
			mux := http.NewServeMux()
			d.registerRoutes(mux)
			if action == "workspace" {
				d.activeNs.runtime = nil // Workspace-only refresh must still report the Git failure.
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate, http.NoBody))
				require.Equal(t, http.StatusInternalServerError, rec.Code)
				require.Contains(t, rec.Body.String(), "authentication required")
			} else {
				d.updateAndStartAsync(action == "force-start", "ns")
				require.Eventually(t, func() bool { return d.updateLastFailure.Load() != nil }, 5*time.Second, 5*time.Millisecond)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody))
				require.Equal(t, http.StatusOK, rec.Code)
				var dto api.NamespaceDto
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
				require.Contains(t, dto.UpdateError, "authentication required")
				require.NotZero(t, dto.UpdateErrorAt)
			}
			if d.activeNs.runtime != nil {
				require.Equal(t, namespace.NsStatusStopped, d.activeNs.runtime.Status(), "failed update must not start cached apps")
			}
		})
	}
}

func TestQueuedPlainUpdateDiscardsEarlierCacheChoice(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "ns", namespace.NsStatusStopped)
	d.activeNs.workspaceID = "private"
	got := make(chan []string, 1)
	d.reloadExFn = func(_, _, _ bool) error { got <- append([]string(nil), d.cachedRepoURLs...); return nil }
	d.reloadMu.Lock()
	d.updateAndStartAsync(false, "ns", cachedRepoChoice{workspaceID: "private", nsID: "ns", URLs: []string{"https://example.invalid/workspace.git"}})
	d.updateAndStartAsync(false, "ns")
	d.reloadMu.Unlock()
	select {
	case urls := <-got:
		require.Empty(t, urls, "a later independent plain click must try the network")
	case <-time.After(5 * time.Second):
		t.Fatal("queued pass never executed")
	}
	require.Eventually(t, func() bool { return !d.updateInFlight.Load() }, 5*time.Second, time.Millisecond)
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock() // join the worker before leaving the test
	require.False(t, d.updatePending.Load(), "completed pass must release its queue slot")
}
