package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
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

// runningForTest reads the lifecycle flag under its lock (test-only helper).
func (s *CloudConfigServer) runningForTest() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.started
}

// TestCloudConfigServerStartStopIdempotent: Start/Stop are safe to call
// repeatedly (the namespace-status lifecycle hook relies on this) and a
// stop releases the port so a later Start can rebind.
func TestCloudConfigServerStartStopIdempotent(t *testing.T) {
	srv := NewCloudConfigServer()
	srv.addr = "127.0.0.1:0" // ephemeral — don't fight for :8761 in tests

	require.NoError(t, srv.Start())
	assert.True(t, srv.runningForTest())
	require.NoError(t, srv.Start(), "second Start is an idempotent no-op")
	assert.True(t, srv.runningForTest())

	srv.Stop()
	assert.False(t, srv.runningForTest())
	srv.Stop() // idempotent no-op when already stopped

	require.NoError(t, srv.Start(), "Start after Stop rebinds")
	assert.True(t, srv.runningForTest())
	srv.Stop()
}

// TestHandleRuntimeEventDrivesCloudConfigLifecycle: a namespace_status event
// starts the cloud-config server unless the namespace is STOPPED, in which case
// it is stopped (so :8761 is released). A nil server must be a no-op.
func TestHandleRuntimeEventDrivesCloudConfigLifecycle(t *testing.T) {
	d := &Daemon{} // broadcastEvent is safe on a zero-value Daemon
	srv := NewCloudConfigServer()
	srv.addr = "127.0.0.1:0"
	defer srv.Stop()

	d.handleRuntimeEvent(api.EventDto{Type: "namespace_status", After: string(namespace.NsStatusStarting)}, srv)
	assert.True(t, srv.runningForTest(), "server starts when the namespace is starting")

	d.handleRuntimeEvent(api.EventDto{Type: "namespace_status", After: string(namespace.NsStatusRunning)}, srv)
	assert.True(t, srv.runningForTest(), "server stays up while running")

	// STOPPING keeps the server up (external debug clients keep config access
	// through a graceful shutdown — only fully STOPPED tears it down).
	d.handleRuntimeEvent(api.EventDto{Type: "namespace_status", After: string(namespace.NsStatusStopping)}, srv)
	assert.True(t, srv.runningForTest(), "server stays up during STOPPING")

	d.handleRuntimeEvent(api.EventDto{Type: "namespace_status", After: string(namespace.NsStatusStopped)}, srv)
	assert.False(t, srv.runningForTest(), "server stops once the namespace is STOPPED")

	// Non-status events and a nil server must not panic or change state.
	d.handleRuntimeEvent(api.EventDto{Type: "app_status", After: "RUNNING"}, srv)
	assert.False(t, srv.runningForTest())
	d.handleRuntimeEvent(api.EventDto{Type: "namespace_status", After: string(namespace.NsStatusRunning)}, nil)
}
