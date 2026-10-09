package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type localS3Docker struct {
	planStubDocker
	port int
}

func (d localS3Docker) GetPublishedPort(context.Context, string, int) int { return d.port }
func (d localS3Docker) GetContainerIP(context.Context, string) string     { return "172.20.0.2" }
func TestContentStorageLocalEnvDesktopOnlyAndPublishedAddress(t *testing.T) {
	for _, tc := range []struct {
		name         string
		desktop      bool
		port, status int
	}{
		{"server refused", false, 8333, http.StatusForbidden},
		{"unpublished refused", true, 0, http.StatusConflict},
		{"desktop export", true, 18333, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetDesktopMode(tc.desktop)
			t.Cleanup(config.ResetDesktopMode)
			rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, localS3Docker{port: tc.port}, t.TempDir())
			t.Cleanup(rt.Shutdown)
			content := &namespace.AppRuntime{Name: appdef.AppContent, Status: namespace.AppStatusStopped, Def: appdef.ApplicationDef{Name: appdef.AppContent}}
			values := map[string]string{
				"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL":         "http://seaweedfs:8333",
				"ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_CREDENTIALS": "content-storage-s3-credentials",
				"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_TYPE":       "BASIC",
				"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME":   "test-access",
				"ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD":   "test-generated-key",
				"ADMIN_PASSWORD": "must-not-export",
			}
			for key, value := range values {
				content.Def.Environments.Set(key, value)
			}
			rt.InjectAppsForTest(content, &namespace.AppRuntime{Name: csProvider, Status: namespace.AppStatusRunning, ContainerID: "provider"})
			d := &Daemon{activeNs: &activeNamespace{nsConfig: csS3Config("ns1"), runtime: rt, workspaceConfig: parseCSWorkspace(t)}}
			w := httptest.NewRecorder()
			d.handleContentStorageLocalEnv(w, httptest.NewRequest("GET", "/", http.NoBody))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			assert.NotContains(t, w.Body.String(), "must-not-export")
			if tc.status == http.StatusOK {
				var env map[string]string
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
				require.Len(t, env, 5)
				assert.Equal(t, "http://127.0.0.1:18333", env["ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL"])
				assert.Equal(t, values["ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD"], env["ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD"])
				assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			} else {
				assert.NotContains(t, w.Body.String(), "test-generated-key")
			}
		})
	}
}
