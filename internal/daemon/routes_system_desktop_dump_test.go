package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/update"
)

// Two desktop artifacts were missing from the diagnostics ZIP, and both were
// needed to diagnose a Windows auto-update rollback:
//
//   - launcher.log — the wrapper's own log, the ONLY record of the daemon-binary
//     selection and of the health-gate verdict, and on Windows the wrapper's
//     only output at all (it is GUI-subsystem and has no stderr),
//   - updates/manifest.json — what was staged and what each payload's verdict
//     was (staged / pending / good / failed).

func dumpZipEntries(t *testing.T, d *Daemon) map[string]string {
	t.Helper()
	rec := httptest.NewRecorder()
	d.writeSystemDumpZip(context.Background(), rec, map[string]any{"daemon": "test"}, nil, nil)
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, openErr := f.Open()
		require.NoError(t, openErr)
		data, readErr := io.ReadAll(rc)
		require.NoError(t, readErr)
		_ = rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

// TestSystemDumpCarriesTheWrapperLogAndUpdateManifest — the desktop case.
func TestSystemDumpCarriesTheWrapperLogAndUpdateManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	require.NoError(t, os.MkdirAll(config.LogDir(), 0o755))
	require.NoError(t, os.WriteFile(config.LauncherLogPath(),
		[]byte("wrapper-marker: selected daemon binary\n"), 0o600))
	// A rotated sibling, exactly as the daemon log block already collects.
	require.NoError(t, os.WriteFile(config.LauncherLogPath()+".1",
		[]byte("wrapper-marker-rotated\n"), 0o600))
	require.NoError(t, update.AddStaged(config.UpdatesDir(), update.Entry{
		Version: "2.13.0",
		Path:    filepath.Join(config.UpdatesDir(), "2.13.0", "citeck"),
		SHA256:  "deadbeef",
	}))

	entries := dumpZipEntries(t, &Daemon{})

	require.Contains(t, entries, "launcher-logs/launcher.log", "the wrapper log must be in the dump")
	require.Contains(t, entries["launcher-logs/launcher.log"], "wrapper-marker: selected daemon binary")
	require.Contains(t, entries, "launcher-logs/launcher.log.1", "rotated wrapper logs too")
	require.Contains(t, entries, "updates/manifest.json", "the update manifest must be in the dump")
	require.Contains(t, entries["updates/manifest.json"], "2.13.0")
	require.Contains(t, entries["updates/manifest.json"], `"state": "staged"`)
}

// TestSystemDumpSucceedsWithoutTheDesktopArtifacts — the server case: neither
// file exists, and a dump must not fail or fabricate them.
func TestSystemDumpSucceedsWithoutTheDesktopArtifacts(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	entries := dumpZipEntries(t, &Daemon{})

	require.Contains(t, entries, "system-info.json", "the dump must still be produced")
	require.NotContains(t, entries, "launcher-logs/launcher.log")
	require.NotContains(t, entries, "updates/manifest.json")
}

// TestSystemDumpTailCapsTheWrapperLog: the wrapper log gets the same treatment
// the daemon log gets — a tail cap, so a rotated-but-huge file cannot blow the
// ZIP up.
func TestSystemDumpTailCapsTheWrapperLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	require.NoError(t, os.MkdirAll(config.LogDir(), 0o755))

	big := bytes.Repeat([]byte("x"), maxDumpLogSize+4096)
	copy(big, "HEAD-MARKER")
	copy(big[len(big)-len("TAIL-MARKER"):], "TAIL-MARKER")
	require.NoError(t, os.WriteFile(config.LauncherLogPath(), big, 0o600))

	got := dumpZipEntries(t, &Daemon{})["launcher-logs/launcher.log"]

	require.Len(t, got, maxDumpLogSize, "the wrapper log must be tail-capped like the daemon log")
	require.Contains(t, got, "TAIL-MARKER", "the tail is what is kept")
	require.NotContains(t, got, "HEAD-MARKER")
}
