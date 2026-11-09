package jvmattach

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The trigger/poll implementation is portable even though Command only enables
// host attach on Linux. Exercise it directly against files and a fake socket;
// never signal a real process or change Supported to increase coverage.
func listenerFixture(t *testing.T) (a *Attacher, trigger, sock string) {
	t.Helper()
	a = New()
	a.ProcRoot = attachSocketDir(t)
	a.SocketWait = time.Second
	a.PollInterval = time.Millisecond
	a.signal = func(int) error {
		t.Fatal("unexpected signal: the fixture must install an explicit fake")
		return nil
	}
	trigger = filepath.Join(a.ProcRoot, "100", "root", "tmp", ".attach_pid7")
	sock = filepath.Join(a.ProcRoot, "100", "root", "tmp", ".java_pid7")
	require.NoError(t, os.MkdirAll(filepath.Dir(trigger), 0o700))
	return a, trigger, sock
}

func TestStartListenerOwnsOnlyTheTriggerItCreates(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new trigger"
		if existing {
			name = "another attach owns the trigger"
		}
		t.Run(name, func(t *testing.T) {
			a, trigger, sock := listenerFixture(t)
			if existing {
				require.NoError(t, os.WriteFile(trigger, []byte("other attach"), 0o600))
			}
			calls := 0
			a.signal = func(pid int) error {
				calls++
				require.Equal(t, 100, pid, "signal the host pid, not namespace pid 7")
				require.FileExists(t, trigger, "the trigger must exist before signaling")
				ln, err := net.Listen("unix", sock)
				require.NoError(t, err)
				t.Cleanup(func() { _ = ln.Close() })
				return nil
			}
			require.NoError(t, a.startListener(context.Background(), 100, 7, sock))
			require.Equal(t, 1, calls)
			if existing {
				contents, err := os.ReadFile(trigger)
				require.NoError(t, err)
				require.Equal(t, "other attach", string(contents))
			} else {
				require.NoFileExists(t, trigger)
			}
		})
	}
}

func TestStartListenerCreateFailureNeverSignals(t *testing.T) {
	a, trigger, sock := listenerFixture(t)
	require.NoError(t, os.Remove(filepath.Dir(trigger)))
	err := a.startListener(context.Background(), 100, 7, sock)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "create attach trigger")
}

func TestStartListenerSignalFailureCleansOwnedTrigger(t *testing.T) {
	a, trigger, sock := listenerFixture(t)
	cause := errors.New("fixture signal refused")
	a.signal = func(pid int) error {
		require.Equal(t, 100, pid)
		require.FileExists(t, trigger)
		return cause
	}
	err := a.startListener(context.Background(), 100, 7, sock)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "signal pid 100")
	require.NoFileExists(t, trigger)
}

func TestStartListenerWaitFailureCleansOwnedTrigger(t *testing.T) {
	for _, failure := range []string{"timeout", "canceled", "context deadline"} {
		t.Run(failure, func(t *testing.T) {
			a, trigger, sock := listenerFixture(t)
			a.signal = func(int) error { return nil }
			ctx := context.Background()
			switch failure {
			case "timeout":
				a.SocketWait = 0
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "context deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			err := a.startListener(ctx, 100, 7, sock)
			if failure == "canceled" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorContains(t, err, "did not start")
			}
			require.NoFileExists(t, trigger)
		})
	}
}

func TestUnsupportedHostAttachNeverReadsProcOrSignals(t *testing.T) {
	if Supported {
		t.Skip("non-Linux host guard")
	}
	a := New()
	a.ProcRoot = filepath.Join(t.TempDir(), "missing-proc")
	a.signal = func(int) error {
		t.Fatal("unsupported host must never signal")
		return nil
	}
	out, err := a.ThreadDump(context.Background(), 100)
	require.ErrorIs(t, err, ErrUnsupported)
	require.Empty(t, out)
	require.ErrorIs(t, sendQuit(100), ErrUnsupported)
}
