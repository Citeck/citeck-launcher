// Tests for the pre-restart diagnostics capture — specifically the thread dump,
// which was empty for every JVM app before this: the runtime images ship a JRE
// (no jcmd) and their PID 1 is `sh -c /entrypoint.sh`, not the JVM, so the old
// `jcmd 1 Thread.print` could not have worked even in a JDK image.
package namespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appfiles"
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

// signalOnlyExec makes every delivery path above SIGQUIT fail: the attach class
// cannot run (no java, as on a non-JVM image or a stripped one), while the
// in-container pid search and the kill still answer. It is how a macOS/Windows
// desktop looks — containers in a VM the host cannot attach into.
func signalOnlyExec(cmd []string) (output string, exitCode int, err error) {
	if cmd[0] == "java" {
		return "sh: java: not found\n", 127, nil
	}
	if strings.Contains(cmd[len(cmd)-1], "/proc/") {
		return "7\n", 0, nil // the JVM is pid 7 inside the container
	}
	return "", 0, nil
}

// The mock's InspectContainer reports no host pid, so the attach path fails and
// the capture falls back through the in-container class to SIGQUIT.
func TestCaptureDiagnostics_SignalFallback(t *testing.T) {
	prev := diagSignalSettle
	diagSignalSettle = 10 * time.Millisecond
	defer func() { diagSignalSettle = prev }()

	base := t.TempDir()
	md := newMockDocker()
	md.logsOut = "…app log…\nFull thread dump OpenJDK 64-Bit Server VM\n"
	md.execFn = signalOnlyExec
	r := newRuntimeForTest(testConfig(), md, base)

	path := r.captureDiagnostics(context.Background(), "emodel", "cid", true, "liveness probe failed 7/7")
	require.NotEmpty(t, path)

	body := readOnlyDiagnosticsFile(t, base, "emodel")
	assert.Contains(t, body, "(sent SIGQUIT")
	assert.NotContains(t, body, "jcmd failed")

	// SIGQUIT must go to the JVM's real pid, not to pid 1 (the entrypoint
	// shell) — signaling the shell dumps nothing at all.
	require.Len(t, md.execCmds, 4)
	assert.Equal(t, findJavaPIDScript, md.execCmds[2][len(md.execCmds[2])-1])
	assert.Equal(t, "kill -3 7", md.execCmds[3][len(md.execCmds[3])-1])

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

// The middle delivery path: the embedded attach client is copied in, run by the
// image's OWN java, and removed — in that order. The class is what makes a real
// dump reachable on a desktop host, where the JVM is not in this host's /proc
// and SIGQUIT would only push the dump into the app's log.
func TestCaptureThreadDump_RunsEmbeddedClassBeforeSignalling(t *testing.T) {
	const dump = "Full thread dump OpenJDK 64-Bit Server VM (25.0.2+10-LTS mixed mode)\n"

	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			return dump, 0, nil
		}
		return "", 0, nil
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	got, inLog, err := r.captureThreadDump(context.Background(), "cid")
	require.NoError(t, err)
	assert.False(t, inLog, "the class returns the dump directly — it must not be sent to the log")
	assert.Equal(t, dump, got)

	// The class is delivered under the name the JVM resolves it by, and it is
	// removed afterwards: nothing the launcher leaves behind in a running
	// production container is acceptable.
	assert.Equal(t, []string{
		"copy " + attachClassDir + "/" + appfiles.AttachClassFileName,
		"exec java -cp " + attachClassDir + " " + appfiles.AttachClassName + " 0 " + attachThreadDumpCmd,
		"exec rm -f " + attachClassDir + "/" + appfiles.AttachClassFileName,
	}, md.opLog)
	require.Len(t, md.copiedFiles, 1)
	classBytes, err := appfiles.AttachClass()
	require.NoError(t, err)
	assert.Equal(t, classBytes, md.copiedFiles[0].data)
}

// A failure of the class path must still clean up, and must still fall through
// to SIGQUIT — the fallback exists precisely for images the class cannot run on.
func TestCaptureThreadDump_CleansUpAndFallsBackWhenClassFails(t *testing.T) {
	prev := diagSignalSettle
	diagSignalSettle = 10 * time.Millisecond
	defer func() { diagSignalSettle = prev }()

	md := newMockDocker()
	md.execFn = signalOnlyExec
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	_, inLog, err := r.captureThreadDump(context.Background(), "cid")
	require.NoError(t, err)
	assert.True(t, inLog, "SIGQUIT delivers the dump through the container log")

	assert.Equal(t, []string{
		"copy " + attachClassDir + "/" + appfiles.AttachClassFileName,
		"exec java -cp " + attachClassDir + " " + appfiles.AttachClassName + " 0 " + attachThreadDumpCmd,
		"exec rm -f " + attachClassDir + "/" + appfiles.AttachClassFileName,
		"exec sh -c " + findJavaPIDScript,
		"exec sh -c kill -3 7",
	}, md.opLog)
}

// An attach client that exits 0 with nothing to say has not produced a dump.
// Reporting that empty string as the dump would replace a usable SIGQUIT
// capture with an empty THREAD DUMP section — the failure mode this whole
// three-path arrangement exists to avoid.
func TestCaptureThreadDump_EmptyClassOutputIsNotADump(t *testing.T) {
	prev := diagSignalSettle
	diagSignalSettle = 10 * time.Millisecond
	defer func() { diagSignalSettle = prev }()

	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if strings.Contains(cmd[len(cmd)-1], "/proc/") {
			return "7\n", 0, nil
		}
		return "  \n", 0, nil // java exits 0, says nothing
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	got, inLog, err := r.captureThreadDump(context.Background(), "cid")
	require.NoError(t, err)
	assert.True(t, inLog)
	assert.Empty(t, got)
}

// When the copy itself fails (read-only /tmp, a container that has just died)
// there is nothing to clean up — and no exec may be spent pretending otherwise.
func TestCaptureThreadDump_SkipsCleanupWhenDeliveryFails(t *testing.T) {
	prev := diagSignalSettle
	diagSignalSettle = 10 * time.Millisecond
	defer func() { diagSignalSettle = prev }()

	md := newMockDocker()
	md.copyErr = errors.New("mkdir /tmp: read-only file system")
	md.execFn = signalOnlyExec
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	_, inLog, err := r.captureThreadDump(context.Background(), "cid")
	require.NoError(t, err)
	assert.True(t, inLog)

	assert.Equal(t, []string{
		"copy " + attachClassDir + "/" + appfiles.AttachClassFileName,
		"exec sh -c " + findJavaPIDScript,
		"exec sh -c kill -3 7",
	}, md.opLog)
}

// With every mechanism gone, the diagnostics file has to say WHY — including
// why the in-container class failed, which is otherwise invisible to whoever
// reads the file after the fact.
func TestCaptureThreadDump_ReportsBothFailures(t *testing.T) {
	md := newMockDocker()
	md.execFn = func(cmd []string) (string, int, error) {
		if cmd[0] == "java" {
			return "sh: java: not found\n", 127, nil
		}
		return "", 1, nil // no jvm found for the signal path either
	}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	_, _, err := r.captureThreadDump(context.Background(), "cid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "java: not found")
	assert.Contains(t, err.Error(), "locate jvm in container")
}

// The in-container search matches on the EXECUTABLE, never on the command line:
// the scanning shell's own cmdline contains the string "java" (the script
// itself does), so a cmdline match returns the shell's pid and the SIGQUIT then
// goes to the wrong process — observed while developing this.
func TestFindJavaPIDScript_MatchesOnExeNotCmdline(t *testing.T) {
	assert.Contains(t, findJavaPIDScript, "readlink")
	assert.NotContains(t, findJavaPIDScript, "cmdline")
}
