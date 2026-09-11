package cli

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/update"
)

// The desktop wrapper's own log and the updater's manifest were the two files
// a Windows auto-update rollback could not be diagnosed without, and neither
// was collected. In THIS archive they must also go through the redactor, like
// every other entry — the wrapper log is a log, and a log is where the launcher
// has historically printed secrets in the clear (`rabbitmqctl add_user citeck
// <pass>`), so exempting a new log file would be the one way to reopen that
// hole.

func collectedEntries(t *testing.T, collect func(dw *dumpWriter)) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	dw := newDumpWriter(&buf)
	collect(dw)
	if err := dw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

// TestDumpCollectsDesktopUpdateArtifacts: both files land in the archive.
func TestDumpCollectsDesktopUpdateArtifacts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	if err := os.MkdirAll(config.LogDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.LauncherLogPath(), []byte("wrapper-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := update.AddStaged(config.UpdatesDir(), update.Entry{
		Version: "2.13.0",
		Path:    filepath.Join(config.UpdatesDir(), "2.13.0", "citeck"),
	}); err != nil {
		t.Fatal(err)
	}

	entries := collectedEntries(t, func(dw *dumpWriter) { collectDesktopUpdateArtifacts(dw, false) })

	if !strings.Contains(entries["desktop/launcher.log"], "wrapper-marker") {
		t.Fatalf("wrapper log missing from the archive: %v", keysOf(entries))
	}
	if !strings.Contains(entries["desktop/update-manifest.json"], "2.13.0") {
		t.Fatalf("update manifest missing from the archive: %v", keysOf(entries))
	}
}

// TestDumpSkipsAbsentDesktopArtifactsSilently: in a server install neither
// file exists. That is not a failed step — it must leave no `.err` entry and no
// errors.json line, or every server dump would carry two invented failures.
func TestDumpSkipsAbsentDesktopArtifactsSilently(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	var buf bytes.Buffer
	dw := newDumpWriter(&buf)
	collectDesktopUpdateArtifacts(dw, false)
	if err := dw.Close(); err != nil {
		t.Fatal(err)
	}
	if len(dw.errs) != 0 {
		t.Fatalf("absent desktop artifacts recorded errors: %+v", dw.errs)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 0 {
		t.Fatalf("absent desktop artifacts produced entries: %v", zr.File)
	}
}

// TestDumpRedactsTheWrapperLog is the redaction contract applied to the new
// entries: a secret value harvested up front must not survive into either of
// them.
func TestDumpRedactsTheWrapperLog(t *testing.T) {
	const secret = "NhpP5EXvkQ3ukKyTbRcrmFZQBEWrpNrK"
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	if err := os.MkdirAll(config.LogDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.LauncherLogPath(),
		[]byte("daemon shutdown POST failed; token="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := update.AddStaged(config.UpdatesDir(), update.Entry{
		Version: "2.13.0",
		Path:    filepath.Join(config.UpdatesDir(), "2.13.0", "citeck"),
		SHA256:  secret,
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	dw := newDumpWriter(&buf)
	r := newSecretRedactor()
	r.addValue(secret)
	r.finalize()
	dw.redactor = r
	collectDesktopUpdateArtifacts(dw, false)
	if err := dw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, openErr := f.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret leaked into %s: %s", f.Name, data)
		}
		if !strings.Contains(string(data), redactPlaceholder) {
			t.Fatalf("%s was not passed through the redactor: %s", f.Name, data)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
