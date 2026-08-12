// Tests for the pre-restart diagnostics capture — specifically the thread dump,
// which was empty for every JVM app before this: the runtime images ship a JRE
// (no jcmd) and their PID 1 is `sh -c /entrypoint.sh`, not the JVM, so the old
// `jcmd 1 Thread.print` could not have worked even in a JDK image.
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
)

func readOnlyDiagnosticsFile(t *testing.T, base, app string) string {
	t.Helper()
	dir := filepath.Join(base, "diagnostics", app)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly one diagnostics file")
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	return string(data)
}

// The mock's InspectContainer reports no host pid, so the attach path fails and
// the capture falls back to SIGQUIT inside the container — the path that also
// runs for real on macOS / Windows desktops, where the containers live in a VM
// the host cannot attach into.
func TestCaptureDiagnostics_SignalFallback(t *testing.T) {
	prev := diagSignalSettle
	diagSignalSettle = 10 * time.Millisecond
	defer func() { diagSignalSettle = prev }()

	base := t.TempDir()
	md := newMockDocker()
	md.logsOut = "…app log…\nFull thread dump OpenJDK 64-Bit Server VM\n"
	md.execFn = func(cmd []string) (string, int, error) {
		if strings.Contains(cmd[len(cmd)-1], "/proc/") {
			return "7\n", 0, nil // the JVM is pid 7 inside the container
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, base)

	path := r.captureDiagnostics(context.Background(), "emodel", "cid", true, "liveness probe failed 7/7")
	require.NotEmpty(t, path)

	body := readOnlyDiagnosticsFile(t, base, "emodel")
	assert.Contains(t, body, "(sent SIGQUIT")
	assert.NotContains(t, body, "jcmd failed")

	// SIGQUIT must go to the JVM's real pid, not to pid 1 (the entrypoint
	// shell) — signaling the shell dumps nothing at all.
	require.Len(t, md.execCmds, 2)
	assert.Equal(t, findJavaPIDScript, md.execCmds[0][len(md.execCmds[0])-1])
	assert.Equal(t, "kill -3 7", md.execCmds[1][len(md.execCmds[1])-1])

	// A stdout-delivered dump is 1500–2500 lines on a real webapp, so the tail
	// must widen — at 500 the file would hold the middle of the dump and
	// neither its start nor the failure that preceded it.
	assert.Equal(t, diagLogTailWithDump, md.logsTail)
	assert.Contains(t, body, "=== LAST 3000 LOG LINES ===")
}

// When neither mechanism works the file must SAY so. The old code wrote
// "(jcmd failed: exit=127)" under the dump heading on every single restart,
// which read as a launcher bug rather than as a missing capability.
func TestCaptureDiagnostics_ReportsUnavailableDump(t *testing.T) {
	base := t.TempDir()
	md := newMockDocker()
	md.execFn = func([]string) (string, int, error) {
		return "", 1, nil // no java process found in the container
	}
	r := newRuntimeForTest(testConfig(), md, base)

	r.captureDiagnostics(context.Background(), "emodel", "cid", true, "crash")

	body := readOnlyDiagnosticsFile(t, base, "emodel")
	assert.Contains(t, body, "=== THREAD DUMP ===\n(unavailable:")
	assert.Contains(t, body, "locate jvm in container")
	// The log section still has to be there — it is the half of the capture
	// that never depended on a JVM.
	assert.Contains(t, body, "=== LAST 500 LOG LINES ===")
	assert.Equal(t, diagLogTailLines, md.logsTail)
}

// A non-Citeck app (postgres, rabbitmq) has no JVM: no dump section, no exec.
func TestCaptureDiagnostics_SkipsThreadDumpForNonJavaApps(t *testing.T) {
	base := t.TempDir()
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, base)

	r.captureDiagnostics(context.Background(), "postgres", "cid", false, "crash")

	body := readOnlyDiagnosticsFile(t, base, "postgres")
	assert.NotContains(t, body, "THREAD DUMP")
	assert.Equal(t, 0, md.execCalls)
}

// The in-container search matches on the EXECUTABLE, never on the command line:
// the scanning shell's own cmdline contains the string "java" (the script
// itself does), so a cmdline match returns the shell's pid and the SIGQUIT then
// goes to the wrong process — observed while developing this.
func TestFindJavaPIDScript_MatchesOnExeNotCmdline(t *testing.T) {
	assert.Contains(t, findJavaPIDScript, "readlink")
	assert.NotContains(t, findJavaPIDScript, "cmdline")
}
