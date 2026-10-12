package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is an isolated production HTTP mux, not daemon.Start: there is no
// runtime, store, Docker client, reconciler, or container lifecycle fixture.
func TestAgentDiscoveryOverUnixSocket(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)
	runDir := t.TempDir()
	t.Setenv("CITECK_RUN", runDir)
	t.Setenv("CITECK_HOST", "")
	listener, err := net.Listen("unix", config.SocketPath())
	require.NoError(t, err)
	mux := http.NewServeMux()
	(&Daemon{version: "agent-socket-test"}).registerRoutes(mux)
	srv := httptest.NewUnstartedServer(mux)
	require.NoError(t, srv.Listener.Close())
	srv.Listener = listener
	srv.Start()
	defer srv.Close()
	c, err := client.New(client.Options{})
	require.NoError(t, err)
	defer c.Close()
	dto, err := c.GetAgentCapabilities()
	require.NoError(t, err)
	if output := os.Getenv("CITECK_AGENT_TEST_OUTPUT"); output != "" {
		raw, encodeErr := json.MarshalIndent(dto, "", "  ")
		require.NoError(t, encodeErr)
		require.NoError(t, os.WriteFile(output, raw, 0o600))
	}
	assert.Equal(t, "server", dto.Mode)
	assert.Equal(t, "agent-socket-test", dto.LauncherVersion)
	op, err := c.GetAgentOperation("namespace.stop")
	require.NoError(t, err)
	assert.Equal(t, api.NamespaceStop, op.Path)
	assert.False(t, op.ReadOnly)

	// Opt-in real binary check after building the server. Only `agent` is
	// executed, against the fixture socket; it cannot start a Docker daemon.
	binary := os.Getenv("CITECK_AGENT_TEST_BINARY")
	if binary == "" {
		return
	}
	require.True(t, filepath.IsAbs(binary))
	cmd := exec.Command(binary, "agent", "application.stop")
	cmd.Env = append(os.Environ(), "CITECK_HOST=", "CITECK_RUN="+runDir, "CITECK_HOME="+runDir, "CITECK_DESKTOP=false")
	out, runErr := cmd.Output()
	require.NoError(t, runErr)
	var actual api.AgentOperationDto
	require.NoError(t, json.Unmarshal(out, &actual))
	assert.Equal(t, "application.stop", actual.ID)
	assert.Equal(t, "POST", actual.Method)
	assert.Equal(t, api.Apps+"/{name}/stop", actual.Path)
	if desktopBinary := os.Getenv("CITECK_AGENT_DESKTOP_TEST_BINARY"); desktopBinary != "" {
		require.True(t, filepath.IsAbs(desktopBinary))
		desktop := exec.Command(desktopBinary, "agent", "application.stop")
		desktop.Env = cmd.Env
		desktopOut, desktopErr := desktop.Output()
		require.NoError(t, desktopErr)
		var desktopOperation api.AgentOperationDto
		require.NoError(t, json.Unmarshal(desktopOut, &desktopOperation))
		assert.Equal(t, actual.ID, desktopOperation.ID)
		assert.Equal(t, actual.Path, desktopOperation.Path)
	}

	// Validate every advertised CLI example against the built command tree,
	// without executing lifecycle operations. --help is inserted before any
	// `--` separator, so exec examples cannot treat it as a container command.
	for _, operation := range dto.Operations {
		for _, example := range operation.CLI {
			words := strings.Fields(example)
			require.GreaterOrEqual(t, len(words), 2)
			require.Equal(t, "citeck", words[0])
			args := append([]string{}, words[1:]...)
			separator := slices.Index(args, "--")
			if separator < 0 {
				args = append(args, "--help")
			} else {
				args = slices.Insert(args, separator, "--help")
			}
			help := exec.Command(binary, args...)
			help.Env = cmd.Env
			text, helpErr := help.CombinedOutput()
			require.NoError(t, helpErr, "invalid CLI example %s: %s", example, text)
			require.NotEmpty(t, text, example)
		}
	}
}
