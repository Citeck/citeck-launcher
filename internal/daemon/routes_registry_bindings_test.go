package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

func TestRegistryBindingEndpoint(t *testing.T) {
	d, mux := secretsTestMux(t)
	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "reg-harbor", Type: storage.SecretRegistryAuth, Host: "harbor.citeck.ru", Username: "u"},
		Value:      "p",
	}))

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", api.RegistryBindings, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	list := func() map[string]string {
		req := httptest.NewRequest("GET", api.RegistryBindings, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var out map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	// Bind a host to an existing secret.
	require.Equal(t, http.StatusOK, post(`{"host":"harbor.citeck.ru","secretId":"reg-harbor"}`).Code)
	assert.Equal(t, map[string]string{"harbor.citeck.ru": "reg-harbor"}, list())

	// Binding to a missing secret is rejected.
	assert.Equal(t, http.StatusNotFound, post(`{"host":"x.io","secretId":"nope"}`).Code)

	// Host is required.
	assert.Equal(t, http.StatusBadRequest, post(`{"host":"","secretId":"reg-harbor"}`).Code)

	// Empty secret id unbinds.
	require.Equal(t, http.StatusOK, post(`{"host":"harbor.citeck.ru","secretId":""}`).Code)
	assert.Empty(t, list())
}

func TestMissingRegistryAuthEndpoint(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("test-master", false))
	d := &Daemon{store: store, secretService: svc, activeNs: &activeNamespace{
		workspaceID: "ws1",
		workspaceConfig: &bundle.WorkspaceConfig{ImageRepos: []bundle.ImageRepo{
			{ID: "core", URL: "nexus.citeck.ru"},                                        // public, no auth
			{ID: "enterprise", URL: "enterprise-registry.citeck.ru", AuthType: "BASIC"}, // needs auth
		}},
	}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	missing := func() []string {
		req := httptest.NewRequest("GET", api.RegistryBindingsMissing, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var out []string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	// The auth-required registry with no credential is reported; the public one is not.
	assert.Equal(t, []string{"enterprise-registry.citeck.ru"}, missing())

	// Provide a credential and bind it → no longer missing.
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "reg", Type: storage.SecretRegistryAuth, Username: "u"},
		Value:      "p",
	}))
	require.NoError(t, store.SetRegistryBinding("ws1", "enterprise-registry.citeck.ru", "reg"))
	assert.Empty(t, missing())

	// Remove the binding → the host re-appears (discriminates a handler that
	// always returns empty from one that actually re-checks resolvability).
	require.NoError(t, store.SetRegistryBinding("ws1", "enterprise-registry.citeck.ru", ""))
	assert.Equal(t, []string{"enterprise-registry.citeck.ru"}, missing())
}

func TestImageRegistryHost(t *testing.T) {
	cases := map[string]string{
		"enterprise-registry.citeck.ru/ecos/app:1.2.3": "enterprise-registry.citeck.ru",
		"nexus.citeck.ru/img":                          "nexus.citeck.ru",
		"localhost:5000/img":                           "localhost:5000",
		"postgres:15":                                  "", // no registry, tag only
		"library/postgres":                             "", // Docker Hub library ref
		"postgres":                                     "", // bare image
		"":                                             "",
	}
	for img, want := range cases {
		assert.Equal(t, want, imageRegistryHost(img), "imageRegistryHost(%q)", img)
	}
}
