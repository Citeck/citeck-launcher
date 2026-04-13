package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDumpSystemInfo_ArchiveStructure exercises the per-step collectors
// against a fake filesystem / fake host commands. We assert that the
// expected top-level directories appear and that the archive round-trips
// (zip reader can open it cleanly). We don't assert exact byte counts —
// that would brittle-fail whenever we add a new step.
func TestDumpSystemInfo_ArchiveStructure(t *testing.T) {
	// Stage a fake CITECK_HOME so config.DaemonLogPath / NamespaceConfigPath
	// point inside our tempdir. The test must not hit the real /opt/citeck.
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)

	// Create the config files the dump expects. Content is deliberately
	// trivial — we're testing file inclusion, not parsing.
	confDir := filepath.Join(tmp, "conf")
	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir log: %v", err)
	}
	writeFile(t, filepath.Join(confDir, "daemon.yml"), "# fake daemon config\nkey: value\n")
	writeFile(t, filepath.Join(confDir, "namespace.yml"), "bundleRef: community:1.0\nproxy:\n  port: 80\n")
	writeFile(t, filepath.Join(logDir, "daemon.log"), generateLines(10))

	// Run in a clean CWD so the archive lands somewhere we own.
	cwd := t.TempDir()
	mustChdir(t, cwd)

	info := BuildInfo{Version: "test-1.0", Commit: "deadbeef", BuildDate: "2026-04-12"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := runDumpSystemInfo(ctx, info, false); err != nil {
		t.Fatalf("runDumpSystemInfo: %v", err)
	}

	// Locate the archive (filename includes a timestamp).
	matches, err := filepath.Glob(filepath.Join(cwd, "citeck-dump-*.zip"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 archive, got %d: %v", len(matches), matches)
	}

	entries := readZipEntries(t, matches[0])

	// Required entries — each proves the corresponding step executed and
	// wrote something (even if it was an `.err` sidecar).
	required := []string{
		"info.txt",
		"citeck/version.txt",
		"citeck/config-view.yaml",
		"daemon/daemon.yml",
		"daemon/namespace.yml",
	}
	for _, want := range required {
		if _, ok := entries[want]; !ok {
			t.Errorf("archive missing required entry %q (entries: %v)", want, keys(entries))
		}
	}

	// Sanity: info.txt should include the injected version.
	if !strings.Contains(entries["info.txt"], "test-1.0") {
		t.Errorf("info.txt missing injected version; got:\n%s", entries["info.txt"])
	}
	// config-view.yaml must be the raw file (not a stripped version).
	if !strings.Contains(entries["citeck/config-view.yaml"], "bundleRef: community:1.0") {
		t.Errorf("config-view.yaml not round-tripped: %s", entries["citeck/config-view.yaml"])
	}
}

// TestDumpSystemInfo_ErrorTolerance verifies that a missing file produces
// an `<entry>.err` sidecar but does not abort the whole command — the
// archive must still be produced.
func TestDumpSystemInfo_ErrorTolerance(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)

	// Note: we deliberately DO NOT create daemon.yml/namespace.yml/daemon.log.
	// Each collectFile / collectTailFile call should record an `.err` entry
	// but the dump must still complete.
	cwd := t.TempDir()
	mustChdir(t, cwd)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := runDumpSystemInfo(ctx, BuildInfo{Version: "dev"}, false); err != nil {
		t.Fatalf("expected no abort despite missing files, got: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(cwd, "citeck-dump-*.zip"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 archive, got %d", len(matches))
	}
	entries := readZipEntries(t, matches[0])

	// At least one `.err` entry must exist (we removed daemon.yml and
	// namespace.yml).
	foundErr := false
	for name := range entries {
		if strings.HasSuffix(name, ".err") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("expected at least one .err sidecar, entries: %v", keys(entries))
	}
	// errors.json is added at the end when any step failed.
	if _, ok := entries["errors.json"]; !ok {
		t.Errorf("expected errors.json aggregate log, entries: %v", keys(entries))
	}
}

// TestTrimHeadTail checks the marker-insertion behavior for bounded logs.
func TestTrimHeadTail(t *testing.T) {
	// 100 lines; head=10, tail=10 → 10 + marker + 10 = 21 lines.
	in := generateLines(100)
	out := trimHeadTail(in, 10, 10)
	if !strings.Contains(out, "skipped 81 lines") {
		// Split keeps a trailing "" element; 100 \n-terminated lines → 101
		// split-elements → trimmed = 101 - 10 - 10 = 81. Matches what the
		// marker should say.
		t.Errorf("marker missing or wrong skip count, got:\n%s", out)
	}
	if !strings.HasPrefix(out, "line-1\n") {
		t.Errorf("head not preserved: %q", out[:20])
	}
}

// TestTrimTail_PreservesShortInput returns input unchanged when it has
// fewer lines than the tail cap.
func TestTrimTail_PreservesShortInput(t *testing.T) {
	in := "a\nb\nc\n"
	if got := trimTail(in, 100); got != in {
		t.Errorf("short input changed: got %q, want %q", got, in)
	}
}

// TestDumpWriter_AddErrRecordsSidecar verifies the `.err` sidecar pattern
// used by every collector when its step fails.
func TestDumpWriter_AddErrRecordsSidecar(t *testing.T) {
	var buf bytes.Buffer
	dw := newDumpWriter(&buf)
	dw.addErr("citeck/status.json", errors.New("daemon down"))
	if err := dw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(dw.errs) != 1 || dw.errs[0].Step != "citeck/status.json" {
		t.Errorf("errs not recorded: %+v", dw.errs)
	}
	entries := readZipReader(t, buf.Bytes())
	if _, ok := entries["citeck/status.json.err"]; !ok {
		t.Errorf(".err entry missing; entries: %v", keys(entries))
	}
}

// TestNewDumpSystemInfoCmd_HasFullFlag guards the CLI surface contract —
// changing the flag name or default would break users' scripts.
func TestNewDumpSystemInfoCmd_HasFullFlag(t *testing.T) {
	cmd := newDumpSystemInfoCmd(BuildInfo{Version: "dev"})
	flag := cmd.Flags().Lookup("full")
	if flag == nil {
		t.Fatal("expected --full flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %s", flag.DefValue)
	}
	if cmd.Use != "dump-system-info" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
}

// --- helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mustChdir changes into dir and restores the original cwd via t.Cleanup.
// Needed because runDumpSystemInfo writes the archive to os.Getwd().
func mustChdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func generateLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line-")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	return b.String()
}

// itoa is intentionally hand-rolled so this test file adds zero imports
// beyond what stdlib already gives us.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func readZipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	return readZipReader(t, data)
}

func readZipReader(t *testing.T, data []byte) map[string]string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("new zip reader: %v", err)
	}
	out := make(map[string]string, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		// Zip entries in tests are small — no need for a size cap.
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
