package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlattenCloudConfig_Nested(t *testing.T) {
	src := map[string]any{
		"spring": map[string]any{
			"datasource": map[string]any{
				"url":      "jdbc:postgresql://localhost/x",
				"username": "u",
			},
		},
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Equal(t, "jdbc:postgresql://localhost/x", out["spring.datasource.url"])
	assert.Equal(t, "u", out["spring.datasource.username"])
	assert.Len(t, out, 2)
}

func TestFlattenCloudConfig_AlreadyFlat(t *testing.T) {
	src := map[string]any{
		"spring.datasource.url": "jdbc:postgresql://localhost/x",
		"server.port":           8080,
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Equal(t, "jdbc:postgresql://localhost/x", out["spring.datasource.url"])
	assert.Equal(t, 8080, out["server.port"])
	assert.Len(t, out, 2)
}

func TestFlattenCloudConfig_ListBracketNotation(t *testing.T) {
	src := map[string]any{
		"spring": map[string]any{
			"profiles": []any{"dev", "local"},
		},
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Equal(t, "dev", out["spring.profiles[0]"])
	assert.Equal(t, "local", out["spring.profiles[1]"])
	assert.Len(t, out, 2)
}

func TestFlattenCloudConfig_EmptyListEmitsEmptyString(t *testing.T) {
	src := map[string]any{
		"spring": map[string]any{
			"profiles": []any{},
		},
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Empty(t, out["spring.profiles"])
}

func TestFlattenCloudConfig_ListOfMaps(t *testing.T) {
	src := map[string]any{
		"items": []any{
			map[string]any{"name": "a", "value": 1},
			map[string]any{"name": "b", "value": 2},
		},
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Equal(t, "a", out["items[0].name"])
	assert.Equal(t, 1, out["items[0].value"])
	assert.Equal(t, "b", out["items[1].name"])
	assert.Equal(t, 2, out["items[1].value"])
}

func TestFlattenCloudConfig_NonStringScalars(t *testing.T) {
	src := map[string]any{
		"server": map[string]any{
			"port":    8080,
			"enabled": true,
			"ratio":   1.5,
			"missing": nil,
		},
	}
	out := map[string]any{}
	flattenCloudConfig(out, src, "")
	assert.Equal(t, 8080, out["server.port"])
	assert.Equal(t, true, out["server.enabled"])
	assert.InEpsilon(t, 1.5, out["server.ratio"], 0.0001)
	assert.Contains(t, out, "server.missing")
	assert.Nil(t, out["server.missing"])
}

func TestUpdateConfig_FlattensNested(t *testing.T) {
	s := NewCloudConfigServer()
	s.UpdateConfig(map[string]map[string]any{
		"emodel": {
			"spring": map[string]any{
				"datasource": map[string]any{
					"url": "jdbc:postgresql://localhost/emodel",
				},
			},
		},
	}, "secret")

	s.mu.RLock()
	defer s.mu.RUnlock()
	require.Contains(t, s.cloudConfig, "emodel")
	assert.Equal(t, "jdbc:postgresql://localhost/emodel", s.cloudConfig["emodel"]["spring.datasource.url"])
}

func TestHandleConfig_ServesFlattenedKeys(t *testing.T) {
	s := NewCloudConfigServer()
	s.UpdateConfig(map[string]map[string]any{
		"emodel": {
			"spring": map[string]any{
				"datasource": map[string]any{
					"url": "jdbc:postgresql://localhost/emodel",
				},
			},
		},
	}, "jwt-secret")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /config/{appName}/{profiles}", s.handleConfig)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/config/emodel/default")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got configResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.PropertySources, 2)
	appSrc := got.PropertySources[1].Source
	assert.Equal(t, "jdbc:postgresql://localhost/emodel", appSrc["spring.datasource.url"])
	_, hasNested := appSrc["spring"]
	assert.False(t, hasNested, "nested spring key should not be present after flattening")
}
