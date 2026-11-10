package namespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/stretchr/testify/require"
)

func TestJVMCommandPreservesEmbeddedPartialOutput(t *testing.T) {
	md := newMockDocker()
	ctx, budget := fsutil.WithOutputLimit(context.Background(), 4)
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			budget.Truncated.Store(true)
			return "part", -1, docker.ErrExecObservationIncomplete
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)
	out, err := r.JVMCommand(ctx, "emodel", "GC.run")
	require.Equal(t, "part", out)
	var dispatched *jvmattach.DispatchError
	require.ErrorAs(t, err, &dispatched)
	require.True(t, dispatched.Uncertain)
	require.ErrorIs(t, err, docker.ErrExecObservationIncomplete)
	require.Len(t, md.execCmds, 2, "one dispatch and cleanup only")
	require.Equal(t, "rm", md.execCmds[1][0])
	r.mu.Lock()
	suspended := r.livenessSuspended["emodel"]
	r.mu.Unlock()
	require.Zero(t, suspended)
}

func TestHeapDumpTruncatedNilErrorDoesNotConfirmExistingFile(t *testing.T) {
	md := newMockDocker()
	base := t.TempDir()
	ctx, budget := fsutil.WithOutputLimit(context.Background(), 4)
	var path string
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			fields := strings.Fields(cmd[len(cmd)-1])
			path = filepath.Join(ExportDirFor(base, "emodel"), filepath.Base(fields[len(fields)-1]))
			require.NoError(t, os.WriteFile(path, []byte("still growing"), 0o600))
			budget.Truncated.Store(true)
			return "part", 0, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, base)
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)
	file, size, err := r.HeapDump(ctx, "emodel", time.Now())
	var dispatched *jvmattach.DispatchError
	require.ErrorAs(t, err, &dispatched)
	require.True(t, dispatched.Uncertain)
	require.Empty(t, file)
	require.Zero(t, size)
	require.FileExists(t, path, "uncertainty must not remove JVM output")
	require.Len(t, md.execCmds, 2)
}

func TestJVMCommandTruncatedNilErrorIsUncertain(t *testing.T) {
	md := newMockDocker()
	ctx, budget := fsutil.WithOutputLimit(context.Background(), 4)
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			budget.Truncated.Store(true)
			return "part", 0, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	seedJVMApp(t, r, "emodel", true, AppStatusRunning)
	out, err := r.JVMCommand(ctx, "emodel", "GC.run")
	require.Equal(t, "part", out)
	var dispatched *jvmattach.DispatchError
	require.ErrorAs(t, err, &dispatched)
	require.True(t, dispatched.Uncertain)
	require.Len(t, md.execCmds, 2)
}
