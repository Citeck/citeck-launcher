package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestCreateRefusesANamespaceWithNoBundle pins the guard behind the reported
// "Quick Start produced only third-party apps" case.
//
// The Citeck services all come from the BUNDLE; the infra apps (postgres,
// mongo, rabbitmq, zookeeper, mailpit, pgadmin, onlyoffice) are emitted by the
// generator unconditionally, each with a hardcoded fallback image. So a
// namespace whose bundle ref is empty generates exactly those seven, starts
// them all, and reports RUNNING 7/7 — green, and without the product in it.
//
// applyDefaultTemplate fills the ref from wsCfg.BundleRepos[0], so an empty ref
// survives only when the workspace config has no bundle repos at all — which is
// what a blank WorkspaceConfig leaves behind (see
// TestResolveEmptyRefKeepsTheWorkspaceConfig). Refuse it at create, the same
// way an unpinnable "LATEST" is already refused, instead of persisting a
// namespace that cannot work.
func TestCreateRefusesANamespaceWithNoBundle(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := testDaemon(t, store)
	d.activeNs = &activeNamespace{
		// A workspace config with NO bundle repos — the shape a blank
		// WorkspaceConfig leaves behind.
		workspaceConfig: &bundle.WorkspaceConfig{},
	}

	_, err = d.buildNamespaceConfigFromCreate(api.NamespaceCreateDto{Name: "Citeck Default"}, "ws1")
	require.Error(t, err, "a namespace with no bundle would run only third-party infra and report RUNNING")

	var cerr *createNamespaceError
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, api.ErrCodeNoBundleConfigured, cerr.code)
}

// The happy path must stay untouched: a workspace with a bundle repo fills the
// ref from the default template and creates normally.
func TestCreateFillsTheBundleRefFromTheWorkspace(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := testDaemon(t, store)
	d.activeNs = &activeNamespace{
		workspaceConfig: &bundle.WorkspaceConfig{
			BundleRepos: []bundle.BundlesRepo{{ID: "community"}},
			NamespaceTemplates: []bundle.NamespaceTemplate{
				{ID: "default", Config: map[string]any{"bundleRef": "community:2026.2"}},
			},
		},
	}

	cfg, err := d.buildNamespaceConfigFromCreate(api.NamespaceCreateDto{Name: "Citeck Default"}, "ws1")
	require.NoError(t, err)
	require.Equal(t, bundle.Ref{Repo: "community", Key: "2026.2"}, cfg.BundleRef)
	require.NotEmpty(t, cfg.ID)
}
