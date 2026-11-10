package docker

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Like the MCP cancellation fixture, this serves a real SDK 101 upgrade, but
// callers deliberately do not install a diagnostic output limit.
func newExecHijackTestClient(t *testing.T, stream func(net.Conn), inspectStatus, exitCode int) *Client {
	return newExecHijackInspectTestClient(t, stream, inspectStatus, exitCode, false)
}

func newExecHijackInspectTestClient(t *testing.T, stream func(net.Conn), inspectStatus, exitCode int, running bool) *Client {
	t.Helper()
	cleanupCtx, cleanup := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/exec"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"Id":"exec1"}`)
		case strings.HasSuffix(r.URL.Path, "/start"):
			_, _ = io.Copy(io.Discard, r.Body)
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			stop := context.AfterFunc(cleanupCtx, func() { _ = conn.Close() })
			defer stop()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buf.Flush()
			stream(conn)
		case strings.HasSuffix(r.URL.Path, "/json"):
			w.WriteHeader(inspectStatus)
			if inspectStatus == http.StatusOK {
				_, _ = io.WriteString(w, `{"ExitCode":`+strconv.Itoa(exitCode)+`,"Running":`+strconv.FormatBool(running)+`}`)
			} else {
				_, _ = io.WriteString(w, `{"message":"inspection unavailable"}`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(cleanup)
	t.Setenv("DOCKER_HOST", strings.Replace(srv.URL, "http://", "tcp://", 1))
	t.Setenv("DOCKER_API_VERSION", "1.54")
	dc, err := NewClient("alpha", "same")
	require.NoError(t, err)
	t.Cleanup(func() { _ = dc.Close() })
	return dc
}

func TestExecInContainerSplitCancelClosesSilentHijack(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			started, closed := make(chan struct{}), make(chan struct{})
			dc := newExecHijackTestClient(t, func(conn net.Conn) {
				close(started)
				_, _ = io.Copy(io.Discard, conn)
				close(closed)
			}, http.StatusOK, 0)
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
				wantErr = context.DeadlineExceeded
			}
			defer cancel()
			type result struct {
				code int
				err  error
			}
			done := make(chan result, 1)
			go func() {
				_, _, code, err := dc.ExecInContainerSplit(ctx, "cid", []string{"probe"})
				done <- result{code, err}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("exec did not upgrade")
			}
			if !deadline {
				cancel()
			}
			select {
			case got := <-done:
				require.ErrorIs(t, got.err, wantErr)
				require.Equal(t, -1, got.code)
			case <-time.After(3 * time.Second):
				t.Fatal("canceled exec retained its reader")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("hijacked connection not closed")
			}
		})
	}
}

func TestExecInContainerSplitTTYFallbackInspectsCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		code    int
		wantErr bool
	}{
		{"success", http.StatusOK, 0, false},
		{"completed failure", http.StatusOK, 7, false},
		{"unavailable inspection", http.StatusInternalServerError, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := newExecHijackTestClient(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "raw tty output without multiplex headers")
			}, tc.status, tc.code)
			_, _, code, err := dc.ExecInContainerSplit(context.Background(), "cid", []string{"probe"})
			if tc.wantErr {
				require.ErrorContains(t, err, "inspection unavailable")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.code, code)
		})
	}
}

func TestExecInContainerSplitCompletedNonzeroExit(t *testing.T) {
	dc := newExecHijackTestClient(t, func(conn net.Conn) {
		// Docker multiplex headers: stream id, padding, big-endian length.
		_, _ = io.WriteString(conn, "\x01\x00\x00\x00\x00\x00\x00\x06output\x02\x00\x00\x00\x00\x00\x00\x0adiagnostic")
	}, http.StatusOK, 7)
	stdout, stderr, code, err := dc.ExecInContainerSplit(context.Background(), "cid", []string{"probe"})
	require.NoError(t, err)
	require.Equal(t, 7, code)
	require.Equal(t, "output", stdout)
	require.Equal(t, "diagnostic", stderr)
}

func TestExecInContainerSplitTTYFallbackReadFailure(t *testing.T) {
	dc := newExecHijackTestClient(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "raw tty output without multiplex headers")
		// Let demultiplexing reject the raw header and the fallback block on
		// the remaining stream before resetting it, with the context still live.
		time.Sleep(50 * time.Millisecond)
		_ = conn.(*net.TCPConn).SetLinger(0)
	}, http.StatusOK, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, code, err := dc.ExecInContainerSplit(ctx, "cid", []string{"probe"})
	require.NoError(t, ctx.Err(), "the read failed independently of cancellation")
	require.Error(t, err)
	require.Equal(t, -1, code)
}
