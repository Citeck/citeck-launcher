package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

// Exercise real resolution and persisted namespace state: ordinary server
// starts and desktop updates retain a removed pinned bundle, fresh bundles
// supersede it, and invalid definitions stay strict on updates. The smaller-set
// safety guard keeps the seeded runtime intact: no Docker lifecycle work.
func TestOrdinaryServerStartKeepsPersistedBundleFallback(t *testing.T) {
	for _, tc := range []struct {
		name                                                  string
		desktop, force, wantError                             bool
		noCache, emptyCache, otherNamespaceCache, freshBundle bool
		sqlite, wrongVersion, invalidBundle                   bool
		latest                                                bool
	}{
		{name: "server start"},
		{name: "server without persisted bundle", noCache: true, wantError: true},
		{name: "server empty persisted bundle", emptyCache: true, wantError: true},
		{name: "server must not use another namespace cache", otherNamespaceCache: true, wantError: true},
		{name: "server fresh bundle without cache", noCache: true, freshBundle: true},
		{name: "server fresh bundle supersedes cached bundle", freshBundle: true},
		{name: "server force succeeds with a fresh bundle", force: true, freshBundle: true},
		{name: "desktop update succeeds with a fresh bundle", desktop: true, freshBundle: true},
		{name: "server explicit Git refresh uses saved bundle", force: true},
		{name: "desktop update and start uses saved bundle", desktop: true},
		{name: "desktop forced update uses SQLite saved bundle", desktop: true, force: true, sqlite: true},
		{name: "desktop update uses SQLite saved bundle", desktop: true, sqlite: true},
		{name: "desktop update without saved bundle fails", desktop: true, sqlite: true, noCache: true, wantError: true},
		{name: "desktop update must not use a different version", desktop: true, sqlite: true, wrongVersion: true, wantError: true},
		{name: "desktop invalid bundle is not hidden by cache", desktop: true, sqlite: true, invalidBundle: true, wantError: true},
		{name: "desktop stored LATEST uses saved bundle when repo is empty", desktop: true, sqlite: true, latest: true},
		{name: "desktop stored LATEST without cache still fails", desktop: true, sqlite: true, latest: true, noCache: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetDesktopMode(tc.desktop)
			t.Cleanup(config.ResetDesktopMode)
			t.Setenv("CITECK_HOME", t.TempDir())
			var store storage.Store
			var err error
			if tc.sqlite {
				db, dbErr := storage.NewSQLiteStore(t.TempDir())
				require.NoError(t, dbErr)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				store = db
			} else {
				store, err = storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
			}
			require.NoError(t, err)
			key := "missing"
			if tc.latest {
				key = "LATEST"
			}
			require.NoError(t, store.SaveNamespaceConfig("", "ns", "Test", "id: ns\nname: Test\nbundleRef: local:"+key+"\nauthentication:\n  type: NONE\n"))
			imported := filepath.Join(config.BundlesDataDir(""), "repo")
			require.NoError(t, os.MkdirAll(filepath.Join(imported, "releases"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(imported, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: local\n    path: releases\n"), 0o600))
			cached := &bundle.Def{Key: bundle.Key{Version: "missing"}, Applications: map[string]bundle.AppDef{appdef.AppEmodel: {Image: "emodel:cached"}}}
			if tc.emptyCache {
				cached = &bundle.Def{}
			}
			if tc.wrongVersion {
				cached.Key.Version = "another-version"
			}
			raw, err := json.Marshal(namespace.NsPersistedState{CachedBundle: cached})
			require.NoError(t, err)
			if !tc.noCache {
				cacheNS := "ns"
				if tc.otherNamespaceCache {
					cacheNS = "another-namespace"
				}
				require.NoError(t, store.SaveNamespaceState("", cacheNS, string(namespace.NsStatusRunning), string(raw)))
			}
			if tc.freshBundle {
				require.NoError(t, os.WriteFile(filepath.Join(imported, "releases", "missing.yml"), []byte("emodel:\n  image: emodel:fresh\n"), 0o600))
			}
			if tc.invalidBundle {
				require.NoError(t, os.WriteFile(filepath.Join(imported, "releases", "missing.yml"), []byte("emodel: ["), 0o600))
			}
			d := newUpdateStartTestDaemon(t, "ns", namespace.NsStatusRunning)
			d.bgCtx = context.Background()
			d.store = store
			d.activeNs.runtime.SetStatePersister(nsStatePersister{store: store, nsID: "ns"})
			if !tc.noCache && !tc.otherNamespaceCache {
				d.activeNs.runtime.RestoreCachedBundle(cached)
			}
			d.secretService, err = storage.NewSecretService(store)
			require.NoError(t, err)
			d.activeNs.workspaceConfig = &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{ID: "local", Path: "releases"}}}
			d.activeNs.volumesBase = t.TempDir()
			apps := make([]*namespace.AppRuntime, 100)
			for i := range apps {
				apps[i] = &namespace.AppRuntime{Name: fmt.Sprintf("existing-%d", i), Status: namespace.AppStatusRunning}
			}
			d.activeNs.runtime.InjectAppsForTest(apps...)
			err = d.invokeReloadEx(tc.force, false, true)
			if tc.wantError {
				switch {
				case tc.invalidBundle:
					require.ErrorContains(t, err, "yaml")
				case tc.latest:
					require.ErrorIs(t, err, bundle.ErrNoBundles)
				default:
					require.ErrorContains(t, err, "bundle missing not found")
				}
				return
			}
			require.NoError(t, err)
			if tc.freshBundle {
				require.Equal(t, "emodel:fresh", d.activeNs.bundleDef.Applications[appdef.AppEmodel].Image)
				stored, present, stateErr := store.LoadNamespaceState("", "ns")
				require.True(t, present)
				require.NoError(t, stateErr)
				var persisted namespace.NsPersistedState
				require.NoError(t, json.Unmarshal([]byte(stored), &persisted))
				require.Equal(t, "emodel:fresh", persisted.CachedBundle.Applications[appdef.AppEmodel].Image)
			} else {
				require.Equal(t, "emodel:cached", d.activeNs.bundleDef.Applications[appdef.AppEmodel].Image)
				require.Len(t, d.activeNs.runtime.ToNamespaceDto().Apps, 100)
			}
			if tc.sqlite && !tc.wantError && !tc.freshBundle {
				// Exercise the actual asynchronous desktop entry point too: a
				// successful fallback must not publish UpdateError to the UI.
				d.recordUpdateFailure("ns", "previous failure")
				rec := httptest.NewRecorder()
				d.handleStartNamespace(rec, httptest.NewRequest(http.MethodPost,
					fmt.Sprintf("/api/v1/namespace/start?force=%t", tc.force), http.NoBody))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Eventually(t, func() bool { return !d.updateInFlight.Load() }, 5*time.Second, 5*time.Millisecond)
				require.Nil(t, d.updateLastFailure.Load())
				rec = httptest.NewRecorder()
				d.handleGetNamespace(rec, httptest.NewRequest(http.MethodGet, "/api/v1/namespace", http.NoBody))
				require.Equal(t, http.StatusOK, rec.Code)
				var dto api.NamespaceDto
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
				require.Empty(t, dto.UpdateError)
				require.Nil(t, dto.UpdateGitError)
			}
		})
	}
}

func TestRemovedBundleUsesSQLiteCacheWithInfoLogOnly(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cached := &bundle.Def{Key: bundle.Key{Version: "2026.3-RC6"}, Applications: map[string]bundle.AppDef{"emodel": {Image: "emodel:saved"}}}
	raw, err := json.Marshal(namespace.NsPersistedState{CachedBundle: cached})
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceState("ws", "ns", "STOPPED", string(raw)))
	data := t.TempDir()
	repo := filepath.Join(data, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "enterprise-rc"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: enterprise-rc\n    path: enterprise-rc\n"), 0o600))
	resolver := bundle.NewResolver(data)
	resolver.SetOffline(true)
	workspace := &bundle.WorkspaceConfig{}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	result, fallback, err := resolveBundleWithCacheFallback(resolver,
		bundle.Ref{Repo: "enterprise-rc", Key: "2026.3-RC6"}, store, "ws", "ns", workspace, "on reload", false)
	require.NoError(t, err)
	require.True(t, fallback)
	cached.SourceRepo = "enterprise-rc" // legacy record is bound in memory, not rewritten
	require.Equal(t, cached, result.Bundle)
	require.Same(t, workspace, result.Workspace)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &entry), "only one INFO record is expected")
	require.Equal(t, "INFO", entry["level"])
	logs.Reset()
	result, fallback, err = resolveBundleWithCacheFallback(resolver,
		bundle.Ref{Repo: "enterprise-rc", Key: "LATEST"}, store, "ws", "ns", workspace, "offline", false)
	require.NoError(t, err)
	require.True(t, fallback)
	require.Equal(t, cached, result.Bundle)
	require.NoError(t, json.Unmarshal(logs.Bytes(), &entry))
	require.Equal(t, "INFO", entry["level"])
	stored, present, err := store.LoadNamespaceState("ws", "ns")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, string(raw), stored, "fallback must not overwrite the saved bundle")
}
