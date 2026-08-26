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

// TestEmptyBundleErrorNamesTheRefAndStaysQuietOtherwise pins the verdict both
// the load path and the reload path derive their bundleError from.
//
// The reload used to clear bundleError unconditionally on success, which is
// right for a namespace whose bundle recovered and silently wrong for one
// edited into resolving to zero applications — that resolve returns no error,
// so the banner could never reach the state it exists for after boot.
func TestEmptyBundleErrorNamesTheRefAndStaysQuietOtherwise(t *testing.T) {
	ref := bundle.Ref{Repo: "community", Key: "2026.2"}

	withApps := &bundle.Def{Applications: map[string]bundle.AppDef{"emodel": {}}}
	require.Empty(t, emptyBundleError(withApps, ref, "ns1"),
		"a bundle carrying Citeck services is not an error")

	empty := emptyBundleError(&bundle.Def{}, ref, "ns1")
	require.NotEmpty(t, empty, "a bundle with zero applications must not be silent")
	require.Contains(t, empty, ref.String(), "the message has to name the ref that resolved to nothing")

	// A nil bundle reaches here only from a resolve failure, which already
	// recorded its own error — do not overwrite it with this one.
	require.Empty(t, emptyBundleError(nil, ref, "ns1"))
}
