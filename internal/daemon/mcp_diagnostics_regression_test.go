package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

// Exercise the actual Docker SDK upgrade, Runtime.JVMCommand and MCP caller,
// not a backend that cooperatively waits on ctx.Done(). The exec stream sends
// no first frame; only closing the hijacked connection can release its reader.
func TestMCPJVMCancelClosesSilentDockerHijackAndReleasesTarget(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	var execs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/cid/json"):
			_, _ = io.WriteString(w, `{"Id":"cid","State":{"Pid":0}}`)
		case strings.HasSuffix(r.URL.Path, "/exec"):
			id := execs.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"Id":"exec%d"}`, id)
		case strings.HasSuffix(r.URL.Path, "/start"):
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buf.Flush()
			if strings.Contains(r.URL.Path, "/exec1/") {
				close(started)
				_, _ = io.Copy(io.Discard, conn)
				close(closed)
			}
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = io.WriteString(w, `{"ExitCode":0}`)
		default:
			w.WriteHeader(http.StatusOK) // copy archive (attach class)
		}
	}))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", strings.Replace(srv.URL, "http://", "tcp://", 1))
	t.Setenv("DOCKER_API_VERSION", "1.54")
	dc, err := docker.NewClient("alpha", "same")
	require.NoError(t, err)
	defer dc.Close()
	d := mcpFixture(t)
	store, err := launchermcp.OpenStore(t.TempDir() + "/state.json")
	require.NoError(t, err)
	d.mcp = launchermcp.New(store, mcpBackend{d})
	defer d.mcp.Close()
	rt := namespace.NewRuntime(d.activeNs.nsConfig, dc, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.InjectAppsForTest(&namespace.AppRuntime{Name: "emodel", ContainerID: "cid", Status: namespace.AppStatusRunning, Def: appdef.ApplicationDef{Name: "emodel", IsJVM: true}})
	d.activeNs.runtime = rt
	logs := captureSyncSlog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() {
		v, e := (mcpBackend{d}).Call(ctx, "jvm_command", launchermcp.Arguments{Target: targetOf(d.active()), App: "emodel", Command: "GC.run"})
		done <- result{v, e}
	}()
	select {
	case <-started:
	case got := <-done:
		t.Fatalf("command returned before upgrade: %v (%v)", got.value, got.err)
	case <-time.After(3 * time.Second):
		t.Fatal("exec did not upgrade")
	}
	require.Contains(t, logs.String(), "Liveness probe suspended")
	require.False(t, d.targetMu.TryLock(), "in-flight command must pin its target")
	cancel()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "unknown", got.value.(map[string]any)["outcome"])
		require.Equal(t, false, got.value.(map[string]any)["replaySafe"])
	case <-time.After(3 * time.Second):
		t.Fatal("canceled hijack retained the MCP call")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("hijacked connection not closed")
	}
	require.Contains(t, logs.String(), "Liveness probe resumed")
	require.True(t, d.targetMu.TryLock(), "completed observation must release target")
	d.targetMu.Unlock()
	require.EqualValues(t, 2, execs.Load(), "client exec plus cleanup, no replay")
}

func TestMCPDiagnosticDispatchVerdicts(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			err := &jvmattach.DispatchError{Err: errors.New("attach reply failed"), Uncertain: uncertain}
			value, gotErr := diagnosticError(context.Background(), fmt.Errorf("wrapped: %w", err))
			if uncertain {
				require.NoError(t, gotErr)
				require.Equal(t, "unknown", value.(map[string]any)["outcome"])
				require.Equal(t, false, value.(map[string]any)["replaySafe"])
			} else {
				var refusal *launchermcp.ToolError
				require.ErrorAs(t, gotErr, &refusal)
				require.Nil(t, value, "an explicit JVM refusal must not be reported completed")
			}
		})
	}
}
