package daemon

import (
	"encoding/json"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const betaBundleYAML = "EcosModelApp:\n  image: core/ecos-model:1.0\n" +
	"experimental:\n" +
	"  ecos-scanner:\n    image: core/ecos-scanner:1.0\n    feature: documentScanner\n    title: Document scanning\n" +
	"  backup-engine:\n    image: core/backup-engine:0.3\n"

// writeBetaBundle puts a bundle with an experimental section on disk for wsID
// (2026.1), beside a later version WITHOUT one (2026.2) — the release that
// graduated the modules.
func writeBetaBundle(t *testing.T, wsID string) {
	t.Helper()
	writeCreatableBundle(t, wsID, "2026.1")
	root := filepath.Join(config.BundlesDataDir(wsID), "repo", "community")
	require.NoError(t, os.WriteFile(filepath.Join(root, "2026.1.yaml"), []byte(betaBundleYAML), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "2026.2.yaml"), []byte("EcosModelApp:\n  image: core/ecos-model:1.1\necos-scanner:\n  image: core/ecos-scanner:1.0\nbackup-engine:\n  image: core/backup-engine:0.3\n"), 0o600))
}

// A create takes its beta features from the REQUEST and from nothing else, and
// every id must be one the chosen bundle offers — a create has no stored list,
// so there is nothing to keep.
func TestCreate_ExperimentalFeaturesComeFromTheRequestAndMustBeOffered(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	writeBetaBundle(t, "ws1")
	d := testDaemon(t, store)
	d.activeNs = &activeNamespace{workspaceConfig: &bundle.WorkspaceConfig{
		BundleRepos: []bundle.BundlesRepo{{ID: "community"}},
		NamespaceTemplates: []bundle.NamespaceTemplate{{
			ID:     "beta",
			Config: map[string]any{"experimentalFeatures": []any{"documentScanner"}},
		}},
	}}
	create := func(req api.NamespaceCreateDto) (*namespace.Config, error) {
		req.Name, req.BundleRepo, req.BundleKey = "test", "community", "2026.1"
		return d.buildNamespaceConfigFromCreate(req, "ws1")
	}

	t.Run("an offered feature is stored, normalized", func(t *testing.T) {
		cfg, err := create(api.NamespaceCreateDto{ExperimentalFeatures: []string{" documentScanner ", "documentScanner"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"documentScanner"}, cfg.ExperimentalFeatures)
	})

	t.Run("one the bundle does not offer is refused, naming what it offers", func(t *testing.T) {
		_, err := create(api.NamespaceCreateDto{ExperimentalFeatures: []string{"documentScanner", "nope"}})
		var cerr *createNamespaceError
		require.ErrorAs(t, err, &cerr)
		assert.Equal(t, http.StatusBadRequest, cerr.status)
		assert.Equal(t, api.ErrCodeExperimentalFeatureUnknown, cerr.code)
		assert.Contains(t, cerr.message, "nope")
		assert.Contains(t, cerr.message, "backup-engine, documentScanner")
	})

	t.Run("the template's own list is ignored", func(t *testing.T) {
		cfg, err := create(api.NamespaceCreateDto{Template: "beta"})
		require.NoError(t, err)
		assert.Nil(t, cfg.ExperimentalFeatures, "a beta module is the operator's choice, never a template's")
	})
}

// The edit form: GET lists what the STORED bundle offers with the namespace's
// choices; PUT takes the state of the offered features and keeps every stored
// id the bundle does not offer — which is what lets a namespace move to the
// release that graduated a module without being refused.
func TestNamespaceEdit_ExperimentalFeatures(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeBetaBundle(t, "wsMain")
	require.NoError(t, d.persistNamespaceConfig("wsMain", "nsbeta", []byte(
		"id: nsbeta\nname: Beta\nbundleRef: community:2026.1\n"+
			"authentication:\n  type: BASIC\n  users: [admin]\nproxy:\n  port: 80\n"+
			"experimentalFeatures: [graduatedLongAgo]\n")))

	getEdit := func() api.NamespaceEditDto {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", api.NamespaceEditPath("nsbeta"), http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var dto api.NamespaceEditDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("PUT", api.NamespaceEditPath("nsbeta"), strings.NewReader(body)))
		return rec
	}
	stored := func() []string {
		cfg, err := d.loadNamespaceConfigFromStore("wsMain", "nsbeta")
		require.NoError(t, err)
		return cfg.ExperimentalFeatures
	}

	t.Run("GET lists the offers of the stored bundle, not the inert id", func(t *testing.T) {
		dto := getEdit()
		assert.Equal(t, []string{"graduatedLongAgo"}, dto.StoredExperimentalFeatures)
		require.Len(t, dto.ExperimentalFeatures, 2)
		assert.Equal(t, "backup-engine", dto.ExperimentalFeatures[0].ID)
		assert.Equal(t, "backup-engine", dto.ExperimentalFeatures[0].Title, "no title falls back to the id")
		assert.Equal(t, "documentScanner", dto.ExperimentalFeatures[1].ID)
		assert.Equal(t, "Document scanning", dto.ExperimentalFeatures[1].Title)
		assert.Equal(t, []string{"ecos-scanner"}, dto.ExperimentalFeatures[1].Apps)
		for _, f := range dto.ExperimentalFeatures {
			assert.False(t, f.Enabled, f.ID)
		}
	})

	t.Run("PUT switches an offered feature on and keeps the inert id", func(t *testing.T) {
		rec := put(`{"enabledExperimentalFeatures":["documentScanner"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored())
		dto := getEdit()
		assert.False(t, dto.ExperimentalFeatures[0].Enabled)
		assert.True(t, dto.ExperimentalFeatures[1].Enabled)
	})

	t.Run("PUT without the field leaves the list alone", func(t *testing.T) {
		rec := put(`{"name":"Beta renamed"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored())
	})

	t.Run("PUT with an id the bundle does not offer is refused and writes nothing", func(t *testing.T) {
		rec := put(`{"enabledExperimentalFeatures":["documentScanner","nope"]}`)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeExperimentalFeatureUnknown)
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored())
	})

	t.Run("a change while the bundle is not on disk is refused", func(t *testing.T) {
		rec := put(`{"bundleRepo":"community","bundleKey":"9.9","enabledExperimentalFeatures":["documentScanner"]}`)
		require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored())
	})

	t.Run("moving to the release that graduated the module is never refused", func(t *testing.T) {
		rec := put(`{"bundleRepo":"community","bundleKey":"2026.2","enabledExperimentalFeatures":[]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored(),
			"both ids are inert on the new bundle and both are kept — a move back finds the choice intact")
		assert.Empty(t, getEdit().ExperimentalFeatures, "the new bundle offers nothing, so the form shows no section")
	})

	t.Run("GET exposes inert IDs and bundle-only rollback preserves their selection", func(t *testing.T) {
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, getEdit().StoredExperimentalFeatures)
		rec := put(`{"bundleRepo":"community","bundleKey":"2026.1"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.True(t, getEdit().ExperimentalFeatures[1].Enabled)
		assert.Equal(t, []string{"graduatedLongAgo", "documentScanner"}, stored())
	})
}

// GET /experimental-features answers for ANY bundle on disk — the create
// dialog asks about a bundle no namespace is on yet — and says plainly when the
// bundle is not there to be asked.
func TestExperimentalFeaturesEndpoint(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	_, mux := newNsCrudTestDaemon(t)
	writeBetaBundle(t, "wsMain")
	get := func(query string) api.ExperimentalFeaturesDto {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", api.ExperimentalFeatures+"?"+query, http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var dto api.ExperimentalFeaturesDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}

	beta := get("workspaceId=wsMain&bundleRepo=community&bundleKey=2026.1")
	assert.Empty(t, beta.Reason)
	require.Len(t, beta.Features, 2)
	assert.Equal(t, "backup-engine", beta.Features[0].ID)
	assert.Equal(t, "documentScanner", beta.Features[1].ID)
	assert.Equal(t, []string{"ecos-scanner"}, beta.Features[1].Apps)

	graduated := get("workspaceId=wsMain&bundleRepo=community&bundleKey=2026.2")
	assert.Empty(t, graduated.Reason)
	assert.Empty(t, graduated.Features)

	unresolved := get("workspaceId=wsMain&bundleRepo=community&bundleKey=9.9")
	assert.Equal(t, api.ExperimentalFeaturesBundleUnresolved, unresolved.Reason)
	assert.Empty(t, unresolved.Features)
}

// An invalid declaration must not be offered by the same API that refuses it
// during namespace validation. The rest of a malformed author's bundle survives.
func TestExperimentalFeaturesEndpointSkipsInvalidIDs(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	_, mux := newNsCrudTestDaemon(t)
	writeBetaBundle(t, "wsMain")
	path := filepath.Join(config.BundlesDataDir("wsMain"), "repo", "community", "2026.1.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(betaBundleYAML, "documentScanner", "document/scanner")), 0o600))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.ExperimentalFeatures+"?workspaceId=wsMain&bundleRepo=community&bundleKey=2026.1", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var dto api.ExperimentalFeaturesDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Len(t, dto.Features, 1)
	assert.Equal(t, "backup-engine", dto.Features[0].ID)
}
