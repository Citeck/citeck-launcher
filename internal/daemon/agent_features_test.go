package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The experimental-features contracts: features.list on the offers endpoint,
// features.enable and features.disable on the namespace edit PUT — two
// contracts on one route, so the route links to the first and the catalog
// lists both. Each example command is one `citeck features` verb.
func TestAgentCatalogDocumentsTheFeaturesOperations(t *testing.T) {
	mux := http.NewServeMux()
	(&Daemon{version: "features-test"}).registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var dto api.AgentCapabilitiesDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))

	ops := make(map[string]api.AgentOperationDto)
	for _, op := range dto.Operations {
		ops[op.ID] = op
	}

	list, ok := ops["features.list"]
	require.True(t, ok)
	assert.Equal(t, http.MethodGet, list.Method)
	assert.Equal(t, api.ExperimentalFeatures, list.Path)
	assert.True(t, list.ReadOnly)
	assert.Equal(t, []string{"citeck features --format json"}, list.CLI)
	params := make([]string, 0, len(list.Parameters))
	for _, p := range list.Parameters {
		params = append(params, p.Name)
	}
	assert.ElementsMatch(t, []string{"bundleRepo", "bundleKey", "workspaceId"}, params)

	for id, verb := range map[string]string{"features.enable": "enable", "features.disable": "disable"} {
		op, ok := ops[id]
		require.True(t, ok, id)
		assert.Equal(t, http.MethodPut, op.Method, id)
		assert.Equal(t, api.NamespaceEdit, op.Path, id)
		assert.False(t, op.ReadOnly, id)
		assert.True(t, op.Async, id)
		assert.False(t, op.Destructive, id)
		assert.Equal(t, []string{"citeck features " + verb + " <id>"}, op.CLI, id)
		assert.Equal(t, "application/json", op.ContentType, id)
		required, _ := op.RequestBody["required"].([]any)
		assert.Equal(t, []any{"enabledExperimentalFeatures"}, required, id)
		require.NotEmpty(t, op.Parameters, id)
		assert.Equal(t, "id", op.Parameters[0].Name, id)
		assert.Contains(t, op.Parameters[0].Description, "Namespace id", id)
		assert.NotEmpty(t, op.Notes, id)
	}

	// The route inventory links the shared PUT to the first contract and
	// still lists the route exactly once.
	var editRoutes []api.AgentRouteDto
	for _, r := range dto.Routes {
		if r.Method == http.MethodPut && r.Path == api.NamespaceEdit {
			editRoutes = append(editRoutes, r)
		}
	}
	require.Len(t, editRoutes, 1)
	assert.Equal(t, "features.enable", editRoutes[0].OperationID)

	// And the per-id lookup answers each of the three.
	for _, id := range []string{"features.list", "features.enable", "features.disable"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities+"/operations/"+id, http.NoBody))
		assert.Equal(t, http.StatusOK, rec.Code, id)
	}
}
