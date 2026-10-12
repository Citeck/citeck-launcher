package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAgentCatalog(t *testing.T, mux http.Handler) api.AgentCapabilitiesDto {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var dto api.AgentCapabilitiesDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	return dto
}

func TestAgentCatalogUsesRegisteredRoutesAndMode(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		mode := "server"
		if desktop {
			mode = "desktop"
		}
		t.Run(mode, func(t *testing.T) {
			config.SetDesktopMode(desktop)
			t.Cleanup(config.ResetDesktopMode)
			mux := http.NewServeMux()
			d := &Daemon{version: "test-version"}
			if desktop {
				d.updateSvc = &update.Service{}
			}
			d.registerRoutes(mux)
			dto := readAgentCatalog(t, mux)
			require.Equal(t, 1, dto.InterfaceVersion)
			assert.Equal(t, mode, dto.Mode)
			assert.Equal(t, "test-version", dto.LauncherVersion)
			assert.Equal(t, config.SocketPath(), dto.SocketPath)
			require.Greater(t, len(dto.Routes), 80)
			seen := map[string]bool{}
			for _, route := range dto.Routes {
				pattern := route.Method + " " + route.Path
				require.False(t, seen[pattern], "duplicate route %s", pattern)
				seen[pattern] = true
				path := strings.NewReplacer("{name}", "emodel", "{id}", "sample", "{file}", "sample.zip", "{path...}", "sample.yml", "{repoId}", "sample").Replace(route.Path)
				_, matched := mux.Handler(httptest.NewRequest(route.Method, path, http.NoBody))
				assert.Equal(t, pattern, matched, "catalog must describe the route actually selected by the production mux")
			}
			for _, operation := range dto.Operations {
				require.True(t, seen[operation.Method+" "+operation.Path], operation.ID)
				if operation.ID == "namespace.activate" {
					assert.Equal(t, desktop, operation.Supported)
				}
			}
			assert.Equal(t, desktop, seen["POST "+api.DesktopUpdateCheck])
			assert.Contains(t, strings.Join(dto.Instructions, " "), "Do not use docker stop")
		})
	}
}

func TestAgentOperationLookupAndNoDisclosure(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)
	mux := http.NewServeMux()
	(&Daemon{version: "test-version"}).registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities+"/operations/application.stop", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var operation api.AgentOperationDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &operation))
	assert.Equal(t, "POST", operation.Method)
	assert.Equal(t, "/api/v1/apps/{name}/stop", operation.Path)
	assert.False(t, operation.ReadOnly)
	assert.True(t, operation.Async)
	assert.Contains(t, operation.Description, "persist")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities+"/operations/unknown", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	// A zero-value daemon has no store/runtime/vault. Both successful discovery
	// requests above prove that retrieving metadata does not read those values.
}

func TestAgentDiscoveryKeepsAuthenticationBoundary(t *testing.T) {
	mux := http.NewServeMux()
	(&Daemon{}).registerRoutes(mux)
	handler := newAPIAuth(testToken).Middleware(mux)
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, api.AgentCapabilities, http.NoBody)
		if authorized {
			req.Header.Set("Authorization", "Bearer "+testToken)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if authorized {
			assert.Equal(t, http.StatusOK, rec.Code)
		} else {
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		}
	}
}

// Consume the discovered operation and call its real lifecycle handler. A
// stopped namespace refuses start; no Docker call or background daemon is used.
func TestAgentDiscoveredLifecycleRetainsStoppedNamespaceGuard(t *testing.T) {
	mux := newStoppedRuntimeDaemon(t)
	dto := readAgentCatalog(t, mux)
	for _, operation := range dto.Operations {
		if operation.ID != "application.start" {
			continue
		}
		path := strings.ReplaceAll(operation.Path, "{name}", "postgres")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(operation.Method, path, http.NoBody))
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, api.ErrCodeNamespaceNotRunning, decodeErr(t, rec).Code)
		return
	}
	t.Fatal("application.start absent from discovery")
}

func TestAgentDiscoveryRejectsWritesAndUnknownPaths(t *testing.T) {
	mux := http.NewServeMux()
	(&Daemon{}).registerRoutes(mux)
	for _, path := range []string{api.AgentCapabilities, api.AgentCapabilities + "/operations/application.stop"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, http.NoBody))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.AgentCapabilities+"/not-a-contract", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
