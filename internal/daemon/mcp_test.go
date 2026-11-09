package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func mcpFixture(t *testing.T) *Daemon {
	t.Helper()
	d := newUpdateStartTestDaemon(t, "same", namespace.NsStatusStopped)
	d.activeNs.workspaceID = "alpha"
	d.activeNs.volumesBase = t.TempDir()
	def := appdef.ApplicationDef{Name: "emodel", Image: "emodel:1", IsJVM: true, Cmd: []string{"unchanged"}, Ports: []string{"*:443:443", "127.0.0.1:8080:8080"}, Volumes: []string{"./app/emodel/props:/run/java.io/spring-props/"}, Environments: appdef.OrderedMapFromMap(map[string]string{"JAVA_OPTS": "-Xmx1g", "KEEP": "yes"})}
	d.activeNs.runtime.SetGeneratedDefs([]appdef.ApplicationDef{def})
	d.activeNs.runtime.SetLastGenFiles(map[string][]byte{"app/emodel/props/application-launcher.yml": []byte("server:\n  port: 8080\n"), "app/emodel/props/script.yml": []byte("not: eligible"), "app/emodel/props/nested/application.yml": []byte("nested: no")})
	require.NoError(t, os.MkdirAll(filepath.Join(d.activeNs.volumesBase, "app", "emodel", "props"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d.activeNs.volumesBase, "app", "emodel", "props", "application-launcher.yml"), []byte("server:\n  port: 8080\n"), 0o644))
	return d
}
func TestMCPSettingsPreserveOverridesAndPortBoundary(t *testing.T) {
	d := mcpFixture(t)
	rt := d.active().runtime
	base, _, err := effectiveMCPDef(rt, "emodel")
	require.NoError(t, err)
	base.Image = "operator:2"
	base.Cmd = []string{"operator-command"}
	require.NoError(t, rt.UpdateAppDef("emodel", base, true))
	memory := "2g"
	java := "-agentlib:jdwp=transport=dt_socket,address=*:5005,server=y,suspend=n"
	cpu := 0.5
	ports := []string{"[::1]:5005:5005"}
	b := mcpBackend{d}
	args := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Settings: &launchermcp.AppSettings{Memory: &memory, CPU: &cpu, JavaOpts: &java, Ports: &ports}}
	_, err = b.Call(context.Background(), "application_settings_write", args)
	require.NoError(t, err)
	_, got, err := effectiveMCPDef(rt, "emodel")
	require.NoError(t, err)
	require.Equal(t, "operator:2", got.Image)
	require.Equal(t, []string{"operator-command"}, got.Cmd)
	require.Equal(t, base.Volumes, got.Volumes)
	require.Contains(t, got.Ports, "*:443:443")
	require.InDelta(t, 0.5, got.Resources.Limits.CPU, 1e-9)
	for _, p := range []string{"*:5005:5005", "0.0.0.0:5005:5005", "[::]:5005:5005", "192.0.2.1:5005:5005", "localhost:5005:5005", "5005"} {
		ports = []string{p}
		args.Settings.Ports = &ports
		before := rt.AppPatch("emodel")
		_, err = b.Call(context.Background(), "application_settings_write", args)
		require.Error(t, err, p)
		require.JSONEq(t, string(before), string(rt.AppPatch("emodel")))
	}
	_, err = b.Call(context.Background(), "application_settings_reset", launchermcp.Arguments{Target: args.Target, App: "emodel"})
	require.NoError(t, err)
	_, got, err = effectiveMCPDef(rt, "emodel")
	require.NoError(t, err)
	require.Equal(t, "operator:2", got.Image)
	require.Equal(t, []string{"operator-command"}, got.Cmd)
	// Concurrent native and MCP edits serialize through the same handler seam.
	var wg sync.WaitGroup
	wg.Add(2)
	for _, m := range []string{"3g", "4g"} {
		go func(memory string) {
			defer wg.Done()
			_, _ = b.Call(context.Background(), "application_settings_write", launchermcp.Arguments{Target: args.Target, App: "emodel", Settings: &launchermcp.AppSettings{Memory: &memory}})
		}(m)
	}
	wg.Wait()
	_, got, err = effectiveMCPDef(rt, "emodel")
	require.NoError(t, err)
	require.Equal(t, "operator:2", got.Image)
}
func TestMCPSpringEligibilityAndResetIsolation(t *testing.T) {
	d := mcpFixture(t)
	b := mcpBackend{d}
	a := launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel"}
	out, err := b.Call(context.Background(), "spring_files_list", a)
	require.NoError(t, err)
	require.Equal(t, []string{"app/emodel/props/application-launcher.yml"}, out)
	for _, key := range []string{"app/emodel/props/script.yml", "app/emodel/props/nested/application.yml", "app/emodel/props/application-new.yml", "export/emodel/application.yml", "app/proxy/application.yml", "../application.yml"} {
		a.Path = key
		_, err = b.Call(context.Background(), "spring_file_read", a)
		require.Error(t, err, key)
		_, err = b.Call(context.Background(), "spring_file_reset", a)
		require.Error(t, err, key)
	}
	a.Path = "app/emodel/props/application-launcher.yml"
	a.Content = "server:\n  port: 9090\n"
	_, err = b.Call(context.Background(), "spring_file_write", a)
	require.NoError(t, err)
	require.True(t, d.active().runtime.IsFileEdited(a.Path))
	a.Content = "[invalid"
	_, err = b.Call(context.Background(), "spring_file_write", a)
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(d.active().volumesBase, a.Path))
	require.NoError(t, err)
	require.Contains(t, string(data), "9090")
	_, err = b.Call(context.Background(), "spring_file_reset", a)
	require.NoError(t, err)
	require.False(t, d.active().runtime.IsFileEdited(a.Path))
	data, err = os.ReadFile(filepath.Join(d.active().volumesBase, a.Path))
	require.NoError(t, err)
	require.Contains(t, string(data), "8080")
}
func TestMCPTargetDoesNotCrossWorkspaceAndStopDoesNotWaitForReload(t *testing.T) {
	d := mcpFixture(t)
	b := mcpBackend{d}
	a := launchermcp.Arguments{WorkspaceID: "beta", NamespaceID: "same", App: "emodel"}
	_, err := b.Call(context.Background(), "application_settings_write", a)
	require.ErrorContains(t, err, "TARGET_CHANGED")
	require.Empty(t, d.active().runtime.AppPatch("emodel"))
	entered := make(chan struct{})
	release := make(chan struct{})
	d.reloadFn = func() error { close(entered); <-release; return nil }
	d.active().runtime.SetStatusForTest(namespace.NsStatusRunning)
	go func() {
		_, _ = b.Call(context.Background(), "application_settings_write", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Settings: &launchermcp.AppSettings{}})
	}()
	<-entered
	require.ErrorIs(t, d.SwitchWorkspace("beta"), errWorkspaceBusy)
	done := make(chan error, 1)
	go func() {
		_, e := b.Call(context.Background(), "namespace_stop", launchermcp.Arguments{Target: targetOf(d.active())})
		done <- e
	}()
	select {
	case e := <-done:
		require.NoError(t, e)
	case <-time.After(time.Second):
		t.Fatal("Stop blocked behind reload")
	}
	close(release)
}
func TestMCPManagementRequiresListenerProvenance(t *testing.T) {
	d := mcpFixture(t)
	store, err := launchermcp.OpenStore(privateMCPStorePath(t))
	require.NoError(t, err)
	d.mcpStore = store
	d.mcp = launchermcp.New(store, mcpBackend{d})
	defer d.mcp.Close()
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	for _, path := range []string{"/api/v1/mcp", "/api/v1/mcp/tokens"} {
		for _, method := range []string{"GET", "POST", "PUT"} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{"name":"x","role":"observer"}`))
			r.Host = "localhost"
			r.Header.Set("X-Citeck-CSRF", "1")
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			require.NotEqual(t, 200, w.Code, path)
		}
	}
	w := httptest.NewRecorder()
	unixHandlerChain(mux).ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/mcp/tokens", strings.NewReader(`{"name":"agent","role":"observer"}`)))
	require.Equal(t, 200, w.Code, w.Body.String())
	var created struct {
		Value string            `json:"value"`
		Token launchermcp.Token `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Value)
	require.NotNil(t, created.Token.ExpiresAt)
	require.False(t, d.mcp.Status().Listening, "token creation must not enable MCP")
	w = httptest.NewRecorder()
	unixHandlerChain(mux).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/mcp", http.NoBody))
	require.NotContains(t, w.Body.String(), created.Value)
	require.NotContains(t, w.Body.String(), "hash")
}

type mcpBearer struct{ value string }

func (b mcpBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.value)
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("test HTTP transport: %w", err)
	}
	return resp, nil
}
func TestSDKAgainstRealDaemonBackendAndSystemArtifact(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d := mcpFixture(t)
	store, err := launchermcp.OpenStore(filepath.Join(config.ConfDir(), "mcp-private", "state.json"))
	require.NoError(t, err)
	d.mcpStore = store
	d.mcp = launchermcp.New(store, mcpBackend{d})
	defer d.mcp.Close()
	token, value, err := store.Create("dev", launchermcp.Developer, nil)
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(nil)
	_, p, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	server.Config.Handler = d.mcp.Handler(port)
	server.Start()
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: mcpBearer{value}}
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: httpClient}, nil)
	require.NoError(t, err)
	defer session.Close()
	target := map[string]any{"workspaceId": "alpha", "namespaceId": "same"}
	appArgs := map[string]any{"workspaceId": "alpha", "namespaceId": "same", "app": "emodel"}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, e)
		require.False(t, result.IsError, "%s: %v", name, result.Content)
		return result
	}
	decode := func(result *mcp.CallToolResult, out any) {
		t.Helper()
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), out))
	}
	var discovery struct {
		Target launchermcp.Target `json:"target"`
	}
	decode(call("launcher_status", map[string]any{}), &discovery)
	require.Equal(t, targetOf(d.active()), discovery.Target)
	call("namespace_status", target)
	call("application_settings_read", appArgs)
	// Validate SDK schema -> typed DTO -> real daemon merge, without Docker.
	writeArgs := map[string]any{"workspaceId": "alpha", "namespaceId": "same", "app": "emodel", "settings": map[string]any{"memory": "", "cpu": 0.5, "environment": map[string]any{"KEEP": nil, "SET_ME": "yes"}, "ports": []string{}}}
	call("application_settings_write", writeArgs)
	var settings launchermcp.AppSettings
	decode(call("application_settings_read", appArgs), &settings)
	require.NotContains(t, settings.Environment, "KEEP")
	require.Equal(t, "yes", *settings.Environment["SET_ME"])
	require.Empty(t, *settings.Memory)
	require.InDelta(t, 0.5, *settings.CPU, 1e-9)
	require.Equal(t, []string{"*:443:443"}, *settings.Ports, "existing public binding retained")
	call("application_settings_reset", appArgs)
	settings = launchermcp.AppSettings{}
	decode(call("application_settings_read", appArgs), &settings)
	require.Equal(t, "yes", *settings.Environment["KEEP"])
	require.NotContains(t, settings.Environment, "SET_ME")
	var paths []string
	decode(call("spring_files_list", appArgs), &paths)
	require.Equal(t, []string{"app/emodel/props/application-launcher.yml"}, paths)
	fileArgs := map[string]any{"workspaceId": "alpha", "namespaceId": "same", "app": "emodel", "path": paths[0]}
	call("spring_file_read", fileArgs)
	fileArgs["content"] = "server:\n  port: 9090\n"
	var saved map[string]any
	decode(call("spring_file_write", fileArgs), &saved)
	require.Equal(t, true, saved["applyRequired"])
	delete(fileArgs, "content")
	var content map[string]string
	decode(call("spring_file_read", fileArgs), &content)
	require.Contains(t, content["content"], "9090")
	call("spring_file_reset", fileArgs)
	decode(call("spring_file_read", fileArgs), &content)
	require.Contains(t, content["content"], "8080")
	result := call("system_dump_create", target)
	var payload struct {
		Artifact launchermcp.Artifact `json:"artifact"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &payload))
	require.NotEmpty(t, payload.Artifact.ID)
	response, err := httpClient.Get(server.URL + payload.Artifact.URL)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, payload.Artifact.Size, int64(len(body)))
	require.NotContains(t, string(body), value)
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	for _, entry := range z.File {
		require.NotContains(t, entry.Name, "mcp")
		r, e := entry.Open()
		require.NoError(t, e)
		data, e := io.ReadAll(r)
		require.NoError(t, e)
		require.NoError(t, r.Close())
		require.NotContains(t, string(data), value)
		require.NotContains(t, string(data), "\"hash\"")
	}
	require.NoError(t, store.Revoke(token.ID))
	response, err = httpClient.Get(server.URL + payload.Artifact.URL)
	require.NoError(t, err)
	require.Equal(t, 401, response.StatusCode)
	_ = response.Body.Close()
}
func TestMCPJVMRefusalKeepsTypedGate(t *testing.T) {
	d := mcpFixture(t)
	s, e := launchermcp.OpenStore(privateMCPStorePath(t))
	require.NoError(t, e)
	d.mcp = launchermcp.New(s, mcpBackend{d})
	_, e = (mcpBackend{d}).Call(context.Background(), "jvm_command", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Command: "VM.flags"})
	var refusal *launchermcp.ToolError
	require.ErrorAs(t, e, &refusal)
	require.Equal(t, 409, refusal.Status)
}

func TestMCPQueuedContinuationsPinWorkspaceAndRuntime(t *testing.T) {
	d := mcpFixture(t)
	logs := captureSyncSlog(t)
	d.reloadFn = func() error { t.Error("continuation crossed target"); return nil }
	d.targetMu.Lock()
	d.regenAfterAttachToggleAsync("emodel", "detach")
	d.configMu.Lock()
	d.activeNs.workspaceID = "beta"
	d.configMu.Unlock()
	d.targetMu.Unlock()
	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), "Attach-toggle regeneration skipped: target changed")
	}, time.Second, time.Millisecond)
	d.reloadMu.Lock()
	d.updateAndStartAsync(false, "same")
	d.configMu.Lock()
	d.activeNs.workspaceID = "gamma"
	d.configMu.Unlock()
	d.reloadMu.Unlock()
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "Update and start skipped: target changed") }, time.Second, time.Millisecond)
	require.False(t, d.updatePending.Load())
}

type refusedZipWriter struct{}

func (refusedZipWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestSystemDumpWriterFailurePropagates(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d := mcpFixture(t)
	err := d.streamSystemDumpZip(context.Background(), refusedZipWriter{}, d.active(), map[string]any{"status": "test"}, nil, nil)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}
