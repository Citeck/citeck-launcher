package jvmattach

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func TestAttachDiagnosticOutputIsBoundedAtSocket(t *testing.T) {
	path := filepath.Join(attachSocketDir(t), "attach.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		request := make([]byte, 1024)
		_, _ = conn.Read(request)
		_, _ = io.WriteString(conn, "0\n"+strings.Repeat("x", 64<<10))
	}()
	ctx, budget := fsutil.WithOutputLimit(context.Background(), 1024)
	out, err := New().exchange(ctx, path, "jcmd", []string{"VM.flags"})
	require.NoError(t, err)
	require.LessOrEqual(t, len(out), 1024)
	require.True(t, budget.Truncated.Load())
	<-done
}
