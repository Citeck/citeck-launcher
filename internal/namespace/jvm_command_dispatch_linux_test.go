//go:build linux

package namespace

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/stretchr/testify/require"
)

// A real attach endpoint receives the action then loses/rejects the response.
// The shared fallback policy must not dispatch it again inside the container.
func TestAttachJcmdDoesNotReplayAfterHostDispatch(t *testing.T) {
	for _, reply := range []string{"0", "broken\n", "1\nUnknown command"} {
		t.Run(reply, func(t *testing.T) {
			proc, err := os.MkdirTemp("", "jcmd-dispatch")
			require.NoError(t, err)
			defer os.RemoveAll(proc)
			dir := filepath.Join(proc, "100")
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "root", "tmp"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte("NSpid:\t100\t7\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "maps"), []byte("0-1 r-xp 0 0:0 1 /lib/server/libjvm.so\n"), 0o600))
			ln, err := net.Listen("unix", filepath.Join(dir, "root", "tmp", ".java_pid7"))
			require.NoError(t, err)
			defer ln.Close()
			request := make(chan string, 1)
			go func() {
				conn, e := ln.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				buf := make([]byte, len("1\x00jcmd\x00GC.run\x00\x00\x00"))
				_, _ = io.ReadFull(conn, buf)
				request <- string(buf)
				_, _ = io.WriteString(conn, reply)
			}()
			att := jvmattach.New()
			att.ProcRoot = proc
			host := func(ctx context.Context, _, verb string, args ...string) (string, error) {
				return att.Command(ctx, 100, verb, args...)
			}
			attempts := 0
			fallback := func(context.Context, string, string, ...string) (string, error) { attempts++; return "replayed", nil }
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = attachJcmdVia(ctx, "cid", "GC.run", nil, host, fallback)
			var dispatched *jvmattach.DispatchError
			require.ErrorAs(t, err, &dispatched)
			require.Equal(t, reply != "1\nUnknown command", dispatched.Uncertain)
			require.NoError(t, ctx.Err(), "lost reply is unknown even with a live context")
			require.Zero(t, attempts)
			select {
			case got := <-request:
				require.Equal(t, "1\x00jcmd\x00GC.run\x00\x00\x00", got)
			case <-ctx.Done():
				t.Fatal("no request")
			}
		})
	}
}

func TestAttachJcmdFallbackOnlyBeforeDispatch(t *testing.T) {
	for _, err := range []error{errors.New("unreachable host"), &jvmattach.DispatchError{Err: io.ErrShortWrite, Uncertain: true}, &jvmattach.DispatchError{Err: errors.New("JVM refused")}} {
		attempts := 0
		host := func(context.Context, string, string, ...string) (string, error) { return "", err }
		fallback := func(context.Context, string, string, ...string) (string, error) {
			attempts++
			return "container result", nil
		}
		_, gotErr := attachJcmdVia(context.Background(), "cid", "GC.run", nil, host, fallback)
		if _, dispatched := errors.AsType[*jvmattach.DispatchError](err); dispatched {
			require.Error(t, gotErr)
			require.Zero(t, attempts)
		} else {
			require.NoError(t, gotErr)
			require.Equal(t, 1, attempts)
		}
	}
}
