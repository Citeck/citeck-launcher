package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentCLIReadsExistingDaemonAndReturnsJSON(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		if r.URL.Path == api.AgentCapabilities {
			_ = json.NewEncoder(w).Encode(api.AgentCapabilitiesDto{InterfaceVersion: 1, LauncherVersion: "daemon-version", Mode: "server"})
			return
		}
		_ = json.NewEncoder(w).Encode(api.AgentOperationDto{ID: "application.stop", Method: "POST", Path: "/api/v1/apps/{name}/stop"})
	}))
	defer srv.Close()
	t.Setenv("CITECK_HOST", strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("CITECK_TLS_CERT", "")
	t.Setenv("CITECK_TLS_KEY", "")
	for _, args := range [][]string{nil, {"application.stop"}} {
		cmd := NewRootCmd(BuildInfo{Version: "client-version"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(append([]string{"agent"}, args...))
		require.NoError(t, cmd.Execute())
		var result map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		if len(args) == 0 {
			assert.Equal(t, "daemon-version", result["launcherVersion"])
		} else {
			assert.Equal(t, "application.stop", result["id"])
		}
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{api.AgentCapabilities, api.AgentCapabilities + "/operations/application.stop"}, paths)
}

func TestAgentCLIDoesNotStartDaemonWhenUnavailable(t *testing.T) {
	t.Setenv("CITECK_HOST", "")
	t.Setenv("CITECK_RUN", t.TempDir())
	cmd := newAgentCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect to existing daemon")
	assert.Empty(t, out.String())
}
