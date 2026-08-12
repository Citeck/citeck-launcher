package jvmattach

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeProc(t *testing.T, procRoot string, pid, ppid int, isJVM bool, nsLine string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\tx\n"+nsLine), 0o600))
	maps := "7f0000000000-7f0000001000 r-xp 00000000 00:00 0 [heap]\n"
	if isJVM {
		maps += "7f1000000000-7f1000900000 r-xp 00000000 fd:00 42 /opt/java/openjdk/lib/server/libjvm.so\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "maps"), []byte(maps), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"),
		[]byte(strconv.Itoa(pid)+" (sh -c (x)) S "+strconv.Itoa(ppid)+" 1 1 0 -1 0 0 0\n"), 0o600))
}

// TestNSPid: the LAST field wins — that is the innermost namespace, i.e. the pid
// the containerized JVM knows itself by and the one that names its attach
// socket. Taking the first field would name the socket after the host pid and
// nothing would ever connect.
func TestNSPid(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 100, 1, true, "NSpid:\t100\t7\n")
	writeProc(t, procRoot, 200, 1, true, "") // no NSpid line at all
	a := New()
	a.ProcRoot = procRoot

	ns, err := a.NSPid(100)
	require.NoError(t, err)
	assert.Equal(t, 7, ns)

	// Pre-4.1 kernels and host processes have no NSpid line: the host pid is
	// then the right answer, not an error.
	ns, err = a.NSPid(200)
	require.NoError(t, err)
	assert.Equal(t, 200, ns)

	_, err = a.NSPid(999)
	require.Error(t, err)
}

// TestFindJVM covers the shape the Citeck images actually have: PID 1 is
// `sh -c /entrypoint.sh` and java is its child, which is why the old
// `jcmd 1 Thread.print` could not have worked even with a JDK in the image.
func TestFindJVM(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 1000, 1, false, "")    // container init: sh
	writeProc(t, procRoot, 1007, 1000, true, "")  // java
	writeProc(t, procRoot, 1008, 1000, false, "") // an unrelated sidecar
	a := New()
	a.ProcRoot = procRoot

	pid, err := a.FindJVM(1000, 3)
	require.NoError(t, err)
	assert.Equal(t, 1007, pid)
}

// A plain `exec java …` entrypoint makes init itself the JVM.
func TestFindJVM_RootIsTheJVM(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 2000, 1, true, "")
	a := New()
	a.ProcRoot = procRoot

	pid, err := a.FindJVM(2000, 3)
	require.NoError(t, err)
	assert.Equal(t, 2000, pid)
}

func TestFindJVM_DepthLimitAndAbsence(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 3000, 1, false, "")
	writeProc(t, procRoot, 3001, 3000, false, "")
	writeProc(t, procRoot, 3002, 3001, true, "") // JVM two generations down
	a := New()
	a.ProcRoot = procRoot

	_, err := a.FindJVM(3000, 1)
	require.ErrorIs(t, err, ErrNoJVM)

	pid, err := a.FindJVM(3000, 2)
	require.NoError(t, err)
	assert.Equal(t, 3002, pid)
}

// The comm field is parenthesized and may contain spaces and ')' — parsing from
// the last ')' is the only correct way, and the fixtures above all carry a comm
// designed to break a naive Fields()[3].
func TestParentOf(t *testing.T) {
	procRoot := t.TempDir()
	writeProc(t, procRoot, 4000, 42, false, "")
	a := New()
	a.ProcRoot = procRoot

	ppid, err := a.parentOf(4000)
	require.NoError(t, err)
	assert.Equal(t, 42, ppid)
}

func TestParseResponse(t *testing.T) {
	out, err := parseResponse("0\npayload\nmore\n")
	require.NoError(t, err)
	assert.Equal(t, "payload\nmore\n", out)

	_, err = parseResponse("no newline at all")
	require.Error(t, err)

	_, err = parseResponse("notanumber\nbody")
	require.Error(t, err)
}
