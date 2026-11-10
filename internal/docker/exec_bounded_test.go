package docker

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func execFrame(stream byte, text string) string {
	var h [8]byte
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(text)))
	return string(h[:]) + text
}

func TestExecInContainerSplitBounded(t *testing.T) {
	for _, tc := range []struct {
		name, stream, out, diagnostic string
		cap, exit, status             int
		running, truncated, failure   bool
	}{
		{name: "interleaved", stream: execFrame(1, "ab") + execFrame(2, "cd") + execFrame(1, "ef"), out: "abef", diagnostic: "cd", cap: 32, exit: 7, status: 200},
		{name: "stderr only", stream: execFrame(2, "failure"), diagnostic: "failure", cap: 32, exit: 7, status: 200},
		{name: "cap minus one", stream: execFrame(1, "abc"), out: "abc", cap: 4, status: 200},
		{name: "exact cap EOF", stream: execFrame(1, "abcd"), out: "abcd", cap: 4, status: 200},
		{name: "cap plus one shared", stream: execFrame(1, "ab") + execFrame(2, "cde"), out: "ab", diagnostic: "cd", cap: 4, status: 500, exit: -1, truncated: true, failure: true},
		{name: "huge advertised frame", stream: "\x01\x00\x00\x00\xff\xff\xff\xffabcd", out: "abcd", cap: 4, status: 500, exit: -1, truncated: true, failure: true},
		{name: "empty headers", stream: strings.Repeat(execFrame(1, ""), 50), cap: 4, status: 500, exit: -1, truncated: true, failure: true},
		{name: "short header", stream: "\x01\x00", cap: 32, status: 200, exit: -1, failure: true},
		{name: "short payload", stream: execFrame(1, "abc")[:9], out: "a", cap: 32, status: 200, exit: -1, failure: true},
		{name: "malformed", stream: "\x01\x01\x00\x00\x00\x00\x00\x00", cap: 32, status: 200, exit: -1, failure: true},
		{name: "raw is not exec frames", stream: "unframed output", cap: 32, status: 200, exit: -1, failure: true},
		{name: "still running", stream: execFrame(1, "abc"), out: "abc", cap: 32, status: 200, running: true, exit: -1, failure: true},
		{name: "inspect failure", stream: execFrame(1, "abc"), out: "abc", cap: 32, status: 500, exit: -1, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := newExecHijackInspectTestClient(t, func(conn net.Conn) { _, _ = io.WriteString(conn, tc.stream) }, tc.status, max(tc.exit, 0), tc.running)
			ctx, budget := fsutil.WithOutputLimit(context.Background(), tc.cap)
			out, diagnostic, code, err := dc.ExecInContainerSplit(ctx, "cid", []string{"probe"})
			require.Equal(t, tc.out, out)
			require.Equal(t, tc.diagnostic, diagnostic)
			require.Equal(t, tc.exit, code)
			require.Equal(t, tc.truncated, budget.Truncated.Load())
			if tc.failure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.truncated || tc.running {
				require.ErrorIs(t, err, ErrExecObservationIncomplete)
			}
		})
	}
}

func TestExecInContainerSplitBoundedDoesNotWaitAfterCap(t *testing.T) {
	closed := make(chan struct{})
	dc := newExecHijackTestClient(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, execFrame(1, "abcde"))
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}, http.StatusInternalServerError, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, budget := fsutil.WithOutputLimit(ctx, 4)
	out, _, code, err := dc.ExecInContainerSplit(ctx, "cid", []string{"probe"})
	require.NoError(t, ctx.Err())
	require.ErrorIs(t, err, ErrExecObservationIncomplete)
	require.Equal(t, "abcd", out)
	require.Equal(t, -1, code)
	require.True(t, budget.Truncated.Load())
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("hijack not closed")
	}
}

func TestExecInContainerSplitBoundedCancelSilentRead(t *testing.T) {
	started := make(chan struct{})
	dc := newExecHijackTestClient(t, func(conn net.Conn) { close(started); _, _ = io.Copy(io.Discard, conn) }, 200, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limited, _ := fsutil.WithOutputLimit(ctx, 32)
	done := make(chan error, 1)
	go func() {
		_, _, code, err := dc.ExecInContainerSplit(limited, "cid", []string{"probe"})
		if code != -1 {
			done <- io.ErrNoProgress
		} else {
			done <- err
		}
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("silent reader blocked")
	}
}
