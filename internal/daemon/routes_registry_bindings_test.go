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
