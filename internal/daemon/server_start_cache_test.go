package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

// Exercise real resolution and persisted namespace state: ordinary server
// starts may use their own usable cache, fresh bundles supersede it, and explicit
// Git/desktop refreshes stay strict. The smaller-set safety guard keeps the
// seeded runtime intact, so this test performs no Docker lifecycle work.
func TestOrdinaryServerStartKeepsPersistedBundleFallback(t *testing.T) {
	for _, tc := range []struct {
		name                                                  string
		desktop, force, wantError                             bool
		noCache, emptyCache, otherNamespaceCache, freshBundle bool
	}{
		{name: "server start"},
		{name: "server without persisted bundle", noCache: true, wantError: true},
		{name: "server empty persisted bundle", emptyCache: true, wantError: true},
		{name: "server must not use another namespace cache", otherNamespaceCache: true, wantError: true},
		{name: "server fresh bundle without cache", noCache: true, freshBundle: true},
		{name: "server fresh bundle supersedes cached bundle", freshBundle: true},
		{name: "server force succeeds with a fresh bundle", force: true, freshBundle: true},
		{name: "desktop update succeeds with a fresh bundle", desktop: true, freshBundle: true},
		{name: "server explicit Git refresh", force: true, wantError: true},
		{name: "desktop update and start", desktop: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetDesktopMode(tc.desktop)
			t.Cleanup(config.ResetDesktopMode)
			t.Setenv("CITECK_HOME", t.TempDir())
			store, err := storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
			require.NoError(t, err)
			require.NoError(t, store.SaveNamespaceConfig("", "ns", "Test", "id: ns\nname: Test\nbundleRef: local:missing\nauthentication:\n  type: NONE\n"))
			imported := filepath.Join(config.BundlesDataDir(""), "repo")
			require.NoError(t, os.MkdirAll(filepath.Join(imported, "releases"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(imported, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: local\n    path: releases\n"), 0o600))
			cached := &bundle.Def{Key: bundle.Key{Version: "missing"}, Applications: map[string]bundle.AppDef{appdef.AppEmodel: {Image: "emodel:cached"}}}
			if tc.emptyCache {
				cached = &bundle.Def{}
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
				require.ErrorContains(t, err, "bundle missing not found")
			} else {
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
			}
		})
	}
}
