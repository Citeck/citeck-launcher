package namespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// seedJVMApp puts one RUNNING app into the runtime. isJVM=false models the
// proxy (KindCiteckCore, but nginx) and the infrastructure containers.
func seedJVMApp(t *testing.T, r *Runtime, name string, isJVM bool, status AppRuntimeStatus) {
	t.Helper()
	def := simpleApp(name, "img:1")
	def.IsJVM = isJVM
	containerID := "cid-" + name
	if status != AppStatusRunning {
		containerID = ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = NsStatusRunning
	r.apps[name] = &AppRuntime{Name: name, Status: status, ContainerID: containerID, Def: def}
}

// The whole jcmd command line goes into ONE attach argument slot. Measured
// against a running webapp: ("VM.flags", "-all") in separate slots runs a bare
// VM.flags and silently drops the option.
func TestJVMCommand_SendsTheWholeCommandLineAsOneArgument(t *testing.T) {
	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			return "[Global flags]\n", 0, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	out, err := r.JVMCommand(context.Background(), "emodel", "VM.flags", "-all")
	require.NoError(t, err)
	assert.Equal(t, "[Global flags]\n", out)

	require.Len(t, md.execCmds, 2) // the client, then its removal
	javaCmd := md.execCmds[0]
	assert.Equal(t, []string{"java", "-cp", attachClassDir, "CiteckAttach", "0", "jcmd", "VM.flags -all"}, javaCmd)
}

// The liveness probe must be down for the whole command and back up after: a
// GC.heap_dump stops the world for longer than the probe tolerates (measured:
// 3.2s for a 57 MB dump, and that scales with the heap).
func TestJVMCommand_SuspendsLivenessForTheDuration(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	var suspendedDuring bool
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			r.mu.Lock()
			suspendedDuring = r.livenessSuspendedLocked("emodel")
			r.mu.Unlock()
			return "dump\n", 0, nil
		}
		return "", 0, nil
	}

	_, err := r.JVMCommand(context.Background(), "emodel", "Thread.print")
	require.NoError(t, err)
	assert.True(t, suspendedDuring, "the probe must be suspended while the JVM is answering")

	r.mu.Lock()
	still := r.livenessSuspended["emodel"]
	r.mu.Unlock()
	assert.Zero(t, still, "and back up afterwards")
}

// A failing command must not leave the app unwatched.
func TestJVMCommand_ResumesLivenessOnFailure(t *testing.T) {
	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			return "sh: java: not found\n", 127, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	_, err := r.JVMCommand(context.Background(), "emodel", "Thread.print")
	require.Error(t, err)

	r.mu.Lock()
	still := r.livenessSuspended["emodel"]
	r.mu.Unlock()
	assert.Zero(t, still)
}

// "postgres is not a JVM app" — said plainly, with nothing attempted. Kind is
// the wrong test for this (the proxy is KindCiteckCore running nginx), which
// is why the check is on IsJVM.
func TestJVMCommand_RefusesNonJVMAppWithoutTouchingIt(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "postgres", false, AppStatusRunning)

	_, err := r.JVMCommand(context.Background(), "postgres", "Thread.print")
	require.ErrorIs(t, err, ErrNotJVMApp)
	assert.Contains(t, err.Error(), "postgres")
	assert.Zero(t, md.execCalls, "nothing may be attempted against a non-JVM app")
	assert.Empty(t, md.copiedFiles, "and nothing may be copied into it")
}

func TestJVMCommand_RefusesStoppedApp(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusStopped)

	_, err := r.JVMCommand(context.Background(), "emodel", "Thread.print")
	require.ErrorIs(t, err, ErrAppNotRunning)
	assert.Zero(t, md.execCalls)
}

// An app that is up but not healthy is the whole reason this exists. Measured on
// the stand: integrations OOMed on metaspace, kept its container, and the
// launcher reported STARTING for two hours — a RUNNING-only gate made the one
// app worth attaching to the one app that could not be attached to.
func TestJVMCommand_WorksOnAnAppThatIsUpButNotHealthy(t *testing.T) {
	for _, status := range []AppRuntimeStatus{AppStatusStarting, AppStatusFailed, AppStatusUpdating} {
		t.Run(string(status), func(t *testing.T) {
			md := newMockDocker()
			md.execFn = func(cmd []string) (string, int, error) {
				if cmd[0] == "java" {
					return "Full thread dump\n", 0, nil
				}
				return "", 0, nil
			}
			r := newRuntimeForTest(testConfig(), md, t.TempDir())
			seedJVMApp(t, r, "emodel", true, AppStatusRunning)
			r.mu.Lock()
			r.apps["emodel"].Status = status // container still there
			r.mu.Unlock()

			out, err := r.JVMCommand(context.Background(), "emodel", "Thread.print")
			require.NoError(t, err)
			assert.Contains(t, out, "Full thread dump")
		})
	}
}

func TestJVMCommand_UnknownApp(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	_, err := r.JVMCommand(context.Background(), "nosuch", "Thread.print")
	require.ErrorIs(t, err, ErrAppUnknown)
}

// A namespace that is stopped has no r.apps at all, and the distinction the
// operator cares about is still "no such app" versus "not running right now" —
// which the generated definitions can answer.
func TestJVMCommand_StoppedNamespaceAnswersFromGeneratedDefs(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	jvm := simpleApp("emodel", "img:1")
	jvm.IsJVM = true
	r.SetGeneratedDefs([]appdef.ApplicationDef{jvm, simpleApp("postgres", "postgres:17")})

	_, err := r.JVMCommand(context.Background(), "emodel", "Thread.print")
	require.ErrorIs(t, err, ErrAppNotRunning)

	_, err = r.JVMCommand(context.Background(), "postgres", "Thread.print")
	require.ErrorIs(t, err, ErrNotJVMApp)

	assert.Zero(t, md.execCalls)
}

// The dump goes into the export directory, gzipped, under a name stamped with
// when it was TAKEN — the same shape RotateHeapDumps uses, so a manual dump and
// an OOM dump are the same kind of thing to the rotation that bounds the disk.
func TestHeapDump_WritesGzippedIntoTheExportDir(t *testing.T) {
	base := t.TempDir()
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, base)
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	var dumpPath string
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] != "java" {
			return "", 0, nil
		}
		// Stand in for the JVM: create the file the command asked for.
		line := cmd[len(cmd)-1]
		fields := strings.Fields(line)
		inContainer := fields[len(fields)-1]
		dumpPath = filepath.Join(ExportDirFor(base, "emodel"), filepath.Base(inContainer))
		require.NoError(t, os.WriteFile(dumpPath, []byte{0x1f, 0x8b}, 0o600))
		return "Heap dump file created [57843899 bytes in 3.238 secs]\n", 0, nil
	}

	at := time.Date(2026, 8, 12, 9, 43, 12, 0, time.UTC)
	file, size, err := r.HeapDump(context.Background(), "emodel", at)
	require.NoError(t, err)
	assert.Equal(t, "emodel-20260812T094312Z.hprof.gz", file)
	assert.Equal(t, int64(2), size, "the size comes from the file, so the caller need not stat it again")

	line := md.execCmds[0][len(md.execCmds[0])-1]
	assert.Equal(t, "GC.heap_dump -gz=1 "+ExportMountPath+"/"+file, line,
		"gzip is not optional — an uncompressed dump is the size of the live heap")

	assert.FileExists(t, dumpPath)
}

// HotSpot reports "Unable to create …: File exists" as ordinary output with a
// success code, so the file on disk is the verdict — not the prose.
func TestHeapDump_FailsWhenNoFileAppears(t *testing.T) {
	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			return "Unable to create /citeck/export/x.hprof.gz: File exists\n", 0, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	_, _, err := r.HeapDump(context.Background(), "emodel", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "File exists")
}

func TestHeapDump_RefusesNonJVMApp(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "postgres", false, AppStatusRunning)

	_, _, err := r.HeapDump(context.Background(), "postgres", time.Now())
	require.ErrorIs(t, err, ErrNotJVMApp)
	assert.Zero(t, md.execCalls)
}

// The container writes the file, so the directory has to exist and be writable
// by the image's user before the command is sent.
func TestHeapDump_CreatesTheExportDirFirst(t *testing.T) {
	base := t.TempDir()
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, base)
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)

	dir := ExportDirFor(base, "emodel")
	require.NoDirExists(t, dir)

	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			assert.DirExists(t, dir, "the export dir must exist before the JVM is asked to write into it")
		}
		return "", 0, nil
	}
	_, _, _ = r.HeapDump(context.Background(), "emodel", time.Now())
	assert.DirExists(t, dir)
}

func TestAppDefIsJVM_StaysOutOfTheDeploymentHash(t *testing.T) {
	// IsJVM is json:"-" so it cannot reach GetHashInput; if that ever changed,
	// every container would be recreated once for a field that describes the
	// image rather than the deployment.
	a := appdef.ApplicationDef{Name: "emodel", Image: "img:1"}
	b := a
	b.IsJVM = true
	assert.Equal(t, a.GetHash(), b.GetHash())
}
