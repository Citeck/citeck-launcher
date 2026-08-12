//go:build linux

package jvmattach

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeJVM builds a procfs stand-in for one process: /proc/<pid>/{status,maps,stat}
// plus the /proc/<pid>/root/tmp directory the attach socket lives in. Everything
// this package touches is a path, so a real JVM is never needed.
// The fake JVM every test in this file attaches to: host pid 100, known to
// itself as pid 7 inside its container — deliberately different numbers, so a
// path built from the wrong one cannot pass.
const (
	fixturePID   = 100
	fixtureNSPID = 7
	fixturePPID  = 1
)

func fakeJVM(t *testing.T, procRoot string, isJVM bool) {
	pid, nsPID, ppid := fixturePID, fixtureNSPID, fixturePPID
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "root", "tmp"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "status"),
		[]byte("Name:\tjava\nUid:\t1000\t1000\t1000\t1000\nNSpid:\t"+
			strconv.Itoa(pid)+"\t"+strconv.Itoa(nsPID)+"\n"), 0o600))

	maps := "7f0000000000-7f0000001000 r-xp 00000000 00:00 0 [heap]\n"
	if isJVM {
		maps += "7f1000000000-7f1000900000 r-xp 00000000 fd:00 42 /opt/java/openjdk/lib/server/libjvm.so\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "maps"), []byte(maps), 0o600))

	// comm deliberately contains a space and a ')' — /proc/<pid>/stat must be
	// parsed from the LAST ')' or ppid comes out of the comm text.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"),
		[]byte(strconv.Itoa(pid)+" (java -jar (x)) S "+strconv.Itoa(ppid)+" 1 1 0 -1 4194304 0 0\n"), 0o600))
}

// serveAttach starts a fake attach listener on the JVM's socket path and
// returns a channel carrying the exact request bytes it received.
func serveAttach(t *testing.T, sock, reply string) <-chan string {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
		_, _ = io.WriteString(conn, reply)
	}()
	return got
}

func newTestAttacher(t *testing.T) (attacher *Attacher, procRoot string) {
	t.Helper()
	// os.MkdirTemp, not t.TempDir: unix socket paths are capped at ~108 bytes
	// and t.TempDir embeds the (long) test name.
	procRoot, err := os.MkdirTemp("", "jvmattach")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(procRoot) })

	a := New()
	a.ProcRoot = procRoot
	a.SocketWait = 2 * time.Second
	a.PollInterval = 10 * time.Millisecond
	return a, procRoot
}

// TestCommand_ExistingListener pins the wire format and the happy path. A JVM
// that has already been attached to keeps its listener socket, so no signal is
// sent — sending one anyway would be a needless SIGQUIT to a production JVM.
func TestCommand_ExistingListener(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)

	signaled := 0
	a.signal = func(int) error { signaled++; return nil }

	req := serveAttach(t, a.socketPath(fixturePID, fixtureNSPID), "0\nFull thread dump\n\"main\" #1\n")

	out, err := a.ThreadDump(context.Background(), fixturePID)
	require.NoError(t, err)
	assert.Equal(t, "Full thread dump\n\"main\" #1\n", out)
	assert.Equal(t, 0, signaled, "must not signal a JVM whose listener is already up")

	// version NUL command NUL arg0 NUL arg1 NUL arg2 NUL — three argument
	// slots are mandatory even when empty; the JVM reads exactly that many.
	assert.Equal(t, "1\x00threaddump\x00\x00\x00\x00", <-req)
}

// TestCommand_StartsListener covers the trigger dance: create the trigger file,
// SIGQUIT, wait for the socket. The trigger must be gone afterwards — left
// behind, it silently turns an operator's later `kill -3` (thread dump to
// stdout) into an attach-listener start instead.
func TestCommand_StartsListener(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)

	trigger := a.triggerPath(fixturePID, fixtureNSPID)
	var sawTrigger bool
	a.signal = func(int) error {
		_, err := os.Stat(trigger)
		sawTrigger = err == nil
		// The real JVM creates the socket from its signal handler; this fake
		// does it inline, which also exercises the polling loop's first pass.
		serveAttach(t, a.socketPath(fixturePID, fixtureNSPID), "0\ndump\n")
		return nil
	}

	out, err := a.ThreadDump(context.Background(), fixturePID)
	require.NoError(t, err)
	assert.Equal(t, "dump\n", out)
	assert.True(t, sawTrigger, "trigger file must exist when SIGQUIT is sent")
	assert.NoFileExists(t, trigger, "trigger file must be removed after attach")
}

// TestCommand_ListenerNeverStarts pins the timeout: a JVM wedged in a long GC
// pause is exactly the state being captured, so the wait must be bounded and
// the trigger cleaned up regardless.
func TestCommand_ListenerNeverStarts(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)
	a.SocketWait = 50 * time.Millisecond
	a.signal = func(int) error { return nil }

	_, err := a.ThreadDump(context.Background(), fixturePID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not start")
	assert.NoFileExists(t, a.triggerPath(fixturePID, fixtureNSPID))
}

// TestCommand_RefusesNonJVM is the safety property of the whole package:
// SIGQUIT starts an attach listener in a JVM but TERMINATES anything else, so a
// process without libjvm.so must never be signaled.
func TestCommand_RefusesNonJVM(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, false)

	signaled := 0
	a.signal = func(int) error { signaled++; return nil }

	_, err := a.ThreadDump(context.Background(), fixturePID)
	require.ErrorIs(t, err, ErrNotAJVM)
	assert.Equal(t, 0, signaled)
}

// TestCommand_NonZeroCompletionCode: the JVM's own error must surface as an
// error, not as dump text — a diagnostics file saying "Unknown command" under a
// "=== THREAD DUMP ===" heading is worse than one that says the capture failed.
func TestCommand_NonZeroCompletionCode(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)
	a.signal = func(int) error { return nil }
	serveAttach(t, a.socketPath(fixturePID, fixtureNSPID), "101\njava.lang.IllegalArgumentException: Unknown diagnostic command\n")

	out, err := a.Command(context.Background(), fixturePID, "bogus")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "code 101")
	assert.Contains(t, err.Error(), "Unknown diagnostic command")
}

func TestCommand_RejectsNULAndOverlongArgs(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)
	a.signal = func(int) error { return nil }

	_, err := a.Command(context.Background(), fixturePID, "jcmd", "a\x00b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NUL")

	_, err = a.Command(context.Background(), fixturePID, "jcmd", "a", "b", "c", "d")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 3")
}

func TestCommand_ContextCancelled(t *testing.T) {
	a, procRoot := newTestAttacher(t)
	fakeJVM(t, procRoot, true)
	a.signal = func(int) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.ThreadDump(ctx, fixturePID)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
