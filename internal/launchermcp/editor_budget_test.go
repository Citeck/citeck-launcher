package launchermcp

import (
	"context"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type editorBudgetBackend struct{ calls atomic.Int32 }

func (b *editorBudgetBackend) Call(_ context.Context, _ string, a Arguments) (any, error) {
	b.calls.Add(1)
	text := strings.Repeat("x", 100<<10)
	if a.App == "combined" {
		text = strings.Repeat("x", 140<<10)
	}
	if a.App == "escaped" {
		text = strings.Repeat("<", 30<<10)
	}
	return map[string]string{"content": text, "baseline": text}, nil
}

func TestEditorBudgetsIncludeBaselineEscapingAndRequestEnvelope(t *testing.T) {
	require.Equal(t, 256<<10, MaxBytes, "update published editor budgets when this limit changes")
	store, err := OpenStore(testStorePath(t))
	require.NoError(t, err)
	_, token, err := store.Create("developer", Developer, nil)
	require.NoError(t, err)
	backend := &editorBudgetBackend{}
	service := New(store, backend)
	t.Cleanup(service.Close)
	server := httptest.NewUnstartedServer(nil)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	server.Config.Handler = service.Handler(n)
	server.Start()
	t.Cleanup(server.Close)
	session := sdkSession(t, server.URL+"/mcp", token)
	for _, app := range []string{"fits", "combined", "escaped"} {
		t.Run(app, func(t *testing.T) {
			result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "application_file_read", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns", "app": app, "path": "app/test.conf"}})
			require.NoError(t, callErr)
			if app == "fits" {
				require.False(t, result.IsError)
			} else {
				require.True(t, result.IsError)
				require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "too large")
			}
		})
	}
	before := backend.calls.Load()
	// Even though content itself fits, the complete JSON-RPC request does not.
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "application_file_write", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns", "app": "fits", "path": "app/test.conf", "content": strings.Repeat("x", MaxBytes-1)}})
	require.True(t, err != nil || result.IsError)
	require.Equal(t, before, backend.calls.Load(), "oversize envelope must be refused before backend dispatch")
}
