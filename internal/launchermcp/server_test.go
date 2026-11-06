package launchermcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	mu     sync.Mutex
	called []string
}

func (f *fakeBackend) Call(_ context.Context, name string, _ Arguments) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, name)
	return map[string]bool{"accepted": true}, nil
}

type bearerTransport struct{ value string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.value)
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("test HTTP transport: %w", err)
	}
	return resp, nil
}
func sdkSession(t *testing.T, endpoint, value string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: bearerTransport{value}}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestSDKRoleMatrixEveryTool(t *testing.T) {
	for _, role := range []Role{Observer, Developer} {
		t.Run(string(role), func(t *testing.T) {
			store, err := OpenStore(t.TempDir() + "/state.json")
			require.NoError(t, err)
			token, value, err := store.Create("agent", role, nil)
			require.NoError(t, err)
			backend := &fakeBackend{}
			service := New(store, backend)
			server := httptest.NewUnstartedServer(nil)
			_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
			n, _ := strconv.Atoi(port)
			server.Config.Handler = service.Handler(n)
			server.Start()
			defer server.Close()
			s := sdkSession(t, server.URL+"/mcp", value)
			list, err := s.ListTools(context.Background(), nil)
			require.NoError(t, err)
			discovered := map[string]bool{}
			for _, tool := range list.Tools {
				discovered[tool.Name] = true
			}
			for _, tool := range expectedToolContracts {
				allowed := !tool.developer || role == Developer
				require.Equal(t, allowed, discovered[tool.name], tool.name)
				result, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool.name, Arguments: validToolArguments(tool)})
				if allowed {
					require.NoError(t, e, tool.name)
					require.False(t, result.IsError, tool.name)
				} else {
					require.True(t, e != nil || result.IsError, tool.name)
				}
			}
			for _, name := range []string{"exec", "secrets_list", "api_request", "token_create", "snapshot_import", "unknown"} {
				result, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
				require.True(t, e != nil || result.IsError, name)
			}
			// Unknown schema keys, including nested full-def fields, must not be accepted.
			result, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "namespace_status", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns", "url": "http://example.invalid"}})
			require.True(t, e != nil || result.IsError)
			if role == Developer {
				result, e = s.CallTool(context.Background(), &mcp.CallToolParams{Name: "application_settings_write", Arguments: map[string]any{"workspaceId": "ws", "namespaceId": "ns", "app": "emodel", "settings": map[string]any{"image": "bad"}}})
				require.True(t, e != nil || result.IsError)
			}
			require.NoError(t, store.Revoke(token.ID))
			_, err = s.ListTools(context.Background(), nil)
			require.Error(t, err, "revocation must affect an already-connected SDK client")
		})
	}
}
func TestHTTPBoundary(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	_, value, err := store.Create("test", Observer, nil)
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	h := service.Handler(DefaultPort)
	for _, test := range []struct {
		name, host, path, auth, origin, method string
		status                                 int
	}{
		{"missing auth", "127.0.0.1:19380", "/mcp", "", "", "POST", 401},
		{"query token", "127.0.0.1:19380", "/mcp?token=" + value, "", "", "POST", 404},
		{"wrong token", "127.0.0.1:19380", "/mcp", "bad", "", "POST", 401},
		{"dns rebinding", "evil.example:19380", "/mcp", value, "", "POST", 403},
		{"lima lookalike", "host.lima.internal.evil.example:19380", "/mcp", value, "", "POST", 403},
		{"lima wrong port", "host.lima.internal:19381", "/mcp", value, "", "POST", 403},
		{"lima browser", "host.lima.internal:19380", "/mcp", value, "http://host.lima.internal:19380", "POST", 403},
		{"wrong port", "127.0.0.1:19381", "/mcp", value, "", "POST", 403},
		{"browser", "localhost:19380", "/mcp", value, "http://localhost:19380", "POST", 403},
		{"null origin", "localhost:19380", "/mcp", value, "null", "POST", 403},
		{"unauthenticated get", "localhost:19380", "/mcp", "", "", "GET", 401},
		{"no streams", "localhost:19380", "/mcp", value, "", "GET", 405},
		{"no sessions", "localhost:19380", "/mcp", value, "", "DELETE", 405},
		{"no api mux", "localhost:19380", "/api/v1/secrets", value, "", "POST", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
			r.Host = test.host
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: DefaultPort}))
			if test.auth != "" {
				r.Header.Set("Authorization", "Bearer "+test.auth)
			}
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, test.status, w.Code, w.Body.String())
			require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		})
	}
	for _, host := range []string{"127.0.0.1:19380", "localhost:19380", "[::1]:19380", "host.lima.internal:19380"} {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
		r.Host = host
		// A real loopback listener sets this context value. Without it the SDK's
		// own Host gate is skipped, masking a Lima-only 403 in recorder tests.
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: DefaultPort}))
		r.Header.Set("Authorization", "Bearer "+value)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Empty(t, w.Header().Get("Mcp-Session-Id"))
	}
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(strings.Repeat("x", MaxBytes+1)))
	r.Host = "localhost:19380"
	r.Header.Set("Authorization", "Bearer "+value)
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 413, w.Code, w.Body.String())
}
func TestConfigureLoopbackAndBindFailure(t *testing.T) {
	store, err := OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	service := New(store, &fakeBackend{})
	defer service.Close()
	require.False(t, service.Status().Enabled)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	require.Error(t, service.Configure(Settings{Enabled: true, Port: n}))
	require.False(t, store.Settings().Enabled)
	require.False(t, service.Status().Listening)
	require.NoError(t, listener.Close())
	require.NoError(t, service.Configure(Settings{Enabled: true, Port: n}))
	require.True(t, service.Status().Listening)
	require.Equal(t, n, store.Settings().Port)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	require.NoError(t, err)
	require.Contains(t, conn.RemoteAddr().String(), "127.0.0.1:")
	require.NoError(t, conn.Close())
	require.NoError(t, service.Configure(Settings{Enabled: false, Port: n}))
	require.False(t, service.Status().Listening)
	_, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	require.Error(t, err)
	bytes, err := json.Marshal(service.Status())
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "hash")
}
