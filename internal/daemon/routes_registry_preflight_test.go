package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// preflightMux stands up a daemon whose active namespace is LOADED but has
// never been started — the state a namespace is in after a daemon restart, or
// after the secrets gate withheld its auto-start. r.apps is empty; the app
// catalog lives in the generated defs (which is exactly why handleGetNamespace
// backfills from act.appDefs).
func preflightMux(t *testing.T, started bool) *http.ServeMux {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	d := testDaemon(t, store)

	defs := []appdef.ApplicationDef{
		{Name: "emodel", Image: "enterprise-registry.citeck.ru/ecos/emodel:2.41.0"},
		{Name: "postgres", Image: "postgres:17.5"},
	}
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetGeneratedDefs(defs)
	if started {
		live := make([]*namespace.AppRuntime, 0, len(defs))
		for _, d := range defs {
			live = append(live, &namespace.AppRuntime{
				Name: d.Name, Status: namespace.AppStatusRunning, Def: d,
			})
		}
		rt.InjectAppsForTest(live...)
	}

	d.activeNs = &activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "test"},
		appDefs:  defs,
		workspaceConfig: &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
			{ID: "ent", URL: "enterprise-registry.citeck.ru", AuthType: "BASIC"},
		}},
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux
}

func missingHosts(t *testing.T, mux *http.ServeMux) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, api.RegistryBindingsMissing, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var hosts []string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &hosts))
	return hosts
}

// TestRegistryPreflightSeesAppsOfANamespaceThatNeverStarted pins the pre-start
// credential check for the state it matters most in.
//
// The endpoint exists so a missing registry credential "surfaces up front
// instead of stalling the namespace mid-pull", and the UI blocks Start on it.
// But it derived the image list from StartableAppImages(), which reads r.apps —
// and r.apps is EMPTY until the namespace has been started once. So on exactly
// the namespace the check is for (freshly loaded, or withheld by the secrets
// gate) it reported "nothing missing", the user pressed Start, and the pulls
// failed on auth anyway.
func TestRegistryPreflightSeesAppsOfANamespaceThatNeverStarted(t *testing.T) {
	t.Run("never started", func(t *testing.T) {
		require.Equal(t, []string{"enterprise-registry.citeck.ru"},
			missingHosts(t, preflightMux(t, false)),
			"a loaded-but-never-started namespace must still report its auth-required registry")
	})
	t.Run("already running", func(t *testing.T) {
		require.Equal(t, []string{"enterprise-registry.citeck.ru"},
			missingHosts(t, preflightMux(t, true)))
	})
}

// The pre-start check asks per IMAGE whether credentials are needed: a public
// project on a host that also serves an auth-required one must not block the
// start of a namespace that pulls only the public image (the community
// observer on harbor), while an image under the auth-required path still does.
func TestRegistryPreflightLetsAPublicPathOnASharedHostThrough(t *testing.T) {
	mux := func(images ...string) *http.ServeMux {
		store, err := storage.NewSQLiteStore(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })
		d := testDaemon(t, store)
		defs := make([]appdef.ApplicationDef, 0, len(images))
		for i, img := range images {
			defs = append(defs, appdef.ApplicationDef{Name: fmt.Sprintf("app%d", i), Image: img})
		}
		rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, planStubDocker{}, t.TempDir())
		t.Cleanup(rt.Shutdown)
		rt.SetGeneratedDefs(defs)
		d.activeNs = &activeNamespace{
			runtime:  rt,
			nsConfig: &namespace.Config{ID: "test"},
			appDefs:  defs,
			workspaceConfig: &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
				{ID: "enterprise", URL: "harbor.citeck.ru/enterprise", AuthType: "BASIC"},
				{ID: "public", URL: "harbor.citeck.ru/public"},
			}},
		}
		m := http.NewServeMux()
		d.registerRoutes(m)
		return m
	}

	require.Empty(t, missingHosts(t, mux("harbor.citeck.ru/public/citeck-observer:v1.5.2", "postgres:17.5")))
	require.Equal(t, []string{"harbor.citeck.ru"}, missingHosts(t, mux(
		"harbor.citeck.ru/public/citeck-observer:v1.5.2", "harbor.citeck.ru/enterprise/citeck-rag:1.2.2")))
}
