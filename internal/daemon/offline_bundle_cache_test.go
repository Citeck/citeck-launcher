package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

type failCacheIdentityStore struct{ storage.Store }

func (s failCacheIdentityStore) SaveNamespaceState(_, _, _, _ string) error {
	return errors.New("cache identity write refused")
}

func TestLegacyCacheKeepsRepositoryAcrossNamespaceEdit(t *testing.T) {
	for _, active := range []bool{true, false} {
		for _, fail := range []bool{false, true} {
			t.Run(map[bool]string{true: "active", false: "inactive"}[active]+map[bool]string{true: " write fails", false: ""}[fail], func(t *testing.T) {
				config.SetDesktopMode(true)
				t.Cleanup(config.ResetDesktopMode)
				t.Setenv("CITECK_HOME", t.TempDir())
				base, err := storage.NewSQLiteStore(t.TempDir())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, base.Close()) })
				var store storage.Store = base
				writeContentStorageWorkspace(t, "ws1")
				workspace := strings.Replace(csWorkspaceYAML, "contentStorage:\n", "  - id: enterprise\n    path: enterprise\ncontentStorage:\n", 1)
				require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceRepoDir("ws1"), "workspace-v1.yml"), []byte(workspace), 0o600))
				before := csConfigYAML(t, "Saved", csBundleS3, namespace.ContentStorageDB)
				require.NoError(t, store.SaveNamespaceConfig("ws1", "ns1", "Saved", string(before)))
				raw := `{"status":"RUNNING","futureFlag":true,"cachedBundle":{"key":{"version":"2026.3"},"applications":{"emodel":{"image":"community:1.0"}},"futureMetadata":42}}`
				require.NoError(t, store.SaveNamespaceState("ws1", "ns1", "RUNNING", raw))
				cfg, err := namespace.ValidateYAML(before)
				require.NoError(t, err)
				d := &Daemon{store: store, version: "2.18.4", activeNs: &activeNamespace{workspaceID: "ws1"}}
				d.secretService, err = storage.NewSecretService(store)
				require.NoError(t, err)
				if fail {
					store = failCacheIdentityStore{store}
					d.store = store
				}
				if active {
					live := newUpdateStartTestDaemon(t, "ns1", namespace.NsStatusRunning)
					d.activeNs = live.activeNs
					d.activeNs.workspaceID = "ws1"
					d.activeNs.nsConfig = cfg
					d.activeNs.bundleDef = &bundle.Def{Key: bundle.Key{Version: "2026.3"}, Applications: map[string]bundle.AppDef{"emodel": {Image: "community:1.0"}}}
					d.activeNs.runtime.RestoreCachedBundle(d.activeNs.bundleDef)
					d.activeNs.runtime.SetStatePersister(nsStatePersister{store: store, wsID: "ws1", nsID: "ns1"})
				}
				cfg.BundleRef.Repo = "enterprise"
				after, err := namespace.MarshalNamespaceConfig(cfg)
				require.NoError(t, err)
				err = d.persistNamespaceConfig("ws1", "ns1", after)
				if fail {
					require.ErrorContains(t, err, "cache identity write refused")
					stored, ok, readErr := base.LoadNamespaceConfig("ws1", "ns1")
					require.NoError(t, readErr)
					require.True(t, ok)
					require.Equal(t, string(before), stored, "failed provenance write must not change the selected kit")
					return
				}
				require.NoError(t, err)
				persisted, ok, err := base.LoadNamespaceState("ws1", "ns1")
				require.NoError(t, err)
				require.True(t, ok)
				var state namespace.NsPersistedState
				require.NoError(t, json.Unmarshal([]byte(persisted), &state))
				require.Equal(t, "community", state.CachedBundle.SourceRepo)
				if !active {
					require.Contains(t, persisted, `"futureFlag":true`)
					require.Contains(t, persisted, `"futureMetadata":42`)
				}
				rows, err := base.ListNamespaces("ws1")
				require.NoError(t, err)
				require.Equal(t, "RUNNING", rows[0].Status)
				d.activeNs = &activeNamespace{workspaceID: "ws1"} // simulate reopening after restart
				_, err = d.resolveBundleOffline("ws1", cfg.BundleRef, "ns1")
				require.Error(t, err, "old Community cache cannot satisfy the stored Enterprise ref")
			})
		}
	}
}

func TestSavedBundleCannotSubstituteAnotherRepository(t *testing.T) {
	data := t.TempDir()
	repo := filepath.Join(data, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "community"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "enterprise"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: community\n    path: community\n  - id: enterprise\n    path: enterprise\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "community", "2026.3.yaml"), []byte("emodel:\n  image: community:1.0\n"), 0o600))
	resolver := bundle.NewResolver(data)
	resolver.SetOffline(true)
	resolved, err := resolver.Resolve(bundle.Ref{Repo: "community", Key: "2026.3"})
	require.NoError(t, err)
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	raw, err := json.Marshal(namespace.NsPersistedState{CachedBundle: resolved.Bundle})
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceState("ws", "ns", "STOPPED", string(raw)))
	_, fallback, err := resolveBundleWithCacheFallback(resolver,
		bundle.Ref{Repo: "enterprise", Key: "2026.3"}, store, "ws", "ns", resolved.Workspace, "review", false)
	require.Error(t, err, "the same version in another repository is a different kit")
	require.False(t, fallback)
}

func TestOfflineNamespaceReadersKeepRemovedBundle(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	writeContentStorageWorkspace(t, "ws1")
	path := filepath.Join(config.WorkspaceRepoDir("ws1"), "community", csBundleS3+".yaml")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	body = append(body, []byte("minLauncherVersion: '2.16.1'\nexperimental:\n  scanner:\n    apps:\n      scanner:\n        image: scanner:1.0\n")...)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	resolver := bundle.NewResolver(config.BundlesDataDir("ws1"))
	resolver.SetOffline(true)
	ref := bundle.Ref{Repo: "community", Key: csBundleS3}
	resolved, err := resolver.Resolve(ref)
	require.NoError(t, err)
	configYAML := csConfigYAML(t, "Saved", csBundleS3, namespace.ContentStorageDB)
	configYAML = append(configYAML, []byte("experimentalFeatures: [scanner]\n")...)
	require.NoError(t, store.SaveNamespaceConfig("ws1", "ns1", "Saved", string(configYAML)))
	cfg, err := namespace.ValidateYAML(configYAML)
	require.NoError(t, err)
	raw, err := json.Marshal(namespace.NsPersistedState{CachedBundle: resolved.Bundle})
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceState("ws1", "ns1", "STOPPED", string(raw)))
	d := &Daemon{store: store, version: "2.18.4", activeNs: &activeNamespace{
		workspaceID: "ws1", nsConfig: cfg, bundleDef: resolved.Bundle, workspaceConfig: resolved.Workspace,
	}}
	d.secretService, err = storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	for _, active := range []bool{true, false} {
		t.Run(map[bool]string{true: "active", false: "inactive"}[active], func(t *testing.T) {
			if !active {
				d.activeNs = &activeNamespace{workspaceID: "ws1"}
			}
			for _, endpoint := range []string{"/experimental-features", "/content-storage/options"} {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, endpoint+"?workspaceId=ws1&namespaceId=ns1&bundleRepo=community&bundleKey="+csBundleS3, http.NoBody)
				if endpoint == "/experimental-features" {
					d.handleExperimentalFeatures(rec, req)
					var dto api.ExperimentalFeaturesDto
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
					require.Empty(t, dto.Reason)
					require.Len(t, dto.Features, 1)
					require.True(t, dto.Features[0].Enabled)
				} else {
					d.handleContentStorageOptions(rec, req)
					var dto api.ContentStorageOptionsDto
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
					require.True(t, dto.S3.Available)
				}
				require.Equal(t, http.StatusOK, rec.Code)
			}
			// The namespace ID alone is enough, including when it is inactive.
			rec := httptest.NewRecorder()
			d.handleExperimentalFeatures(rec, httptest.NewRequest(http.MethodGet,
				"/experimental-features?workspaceId=ws1&namespaceId=ns1", http.NoBody))
			var offers api.ExperimentalFeaturesDto
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &offers))
			require.Len(t, offers.Features, 1)
			require.True(t, offers.Features[0].Enabled)
			rec = httptest.NewRecorder()
			d.handleContentStorageOptions(rec, httptest.NewRequest(http.MethodGet,
				"/content-storage/options?workspaceId=ws1&namespaceId=ns1", http.NoBody))
			var options api.ContentStorageOptionsDto
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &options))
			require.True(t, options.S3.Available)
			features := d.experimentalFeaturesForEdit(httptest.NewRequest(http.MethodGet, "/", http.NoBody), "ws1", cfg)
			require.Len(t, features, 1)
			d.version = "2.15.0"
			require.NotNil(t, d.launcherFloorRefusal("ws1", cfg), "cached compatibility floor still applies")
			d.version = "2.18.4"
			s3cfg := *cfg
			s3cfg.ContentStorage = &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}
			require.Nil(t, d.contentStorageRefusal("ws1", storedContentStorage{typ: namespace.ContentStorageDB, bundleRef: ref}, &s3cfg))
			require.Contains(t, d.requiredAppsFor("ws1", &s3cfg), "seaweedfs")
		})
	}
	_, err = d.resolveBundleOffline("ws1", ref, "another-namespace")
	require.Error(t, err, "an unrelated namespace must not borrow this cache")
	_, err = d.resolveBundleOffline("another-workspace", ref, "ns1")
	require.Error(t, err, "a different workspace must not borrow this cache")
	_, createErr := d.resolveBundleForCreate("ws1", ref)
	require.NotNil(t, createErr, "creating a namespace still requires its own available bundle")
}
