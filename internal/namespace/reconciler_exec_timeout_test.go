package namespace

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/stretchr/testify/require"
)

func TestRunLivenessProbeSilentExecTimeoutIsUnhealthy(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	cleanupCtx, cleanup := context.WithCancel(context.Background())
	// The same real SDK hijack as the MCP regression, without its output
	// budget: this is an ordinary exec-based namespace liveness probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/exec"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"Id":"probe1"}`)
		case strings.HasSuffix(r.URL.Path, "/start"):
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			stop := context.AfterFunc(cleanupCtx, func() { _ = conn.Close() })
			defer stop()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buf.Flush()
			close(started)
			_, _ = io.Copy(io.Discard, conn)
			close(closed)
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = io.WriteString(w, `{"ExitCode":0}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	defer cleanup()
	t.Setenv("DOCKER_HOST", strings.Replace(srv.URL, "http://", "tcp://", 1))
	t.Setenv("DOCKER_API_VERSION", "1.54")
	dc, err := docker.NewClient("alpha", "same")
	require.NoError(t, err)
	defer dc.Close()
	cfg := DefaultNamespaceConfig()
	rt := NewRuntime(&cfg, dc, t.TempDir())
	t.Cleanup(rt.Shutdown)
	done := make(chan bool, 1)
	go func() {
		done <- rt.runLivenessProbe(context.Background(), "cid", &appdef.AppProbeDef{
			TimeoutSeconds: 1,
			Exec:           &appdef.ExecProbeDef{Command: []string{"probe"}},
		})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not upgrade")
	}
	select {
	case healthy := <-done:
		require.False(t, healthy, "a timed-out exec cannot prove liveness")
	case <-time.After(3 * time.Second):
		t.Fatal("probe timeout did not release its reader")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("probe did not close its hijacked connection")
	}
}
