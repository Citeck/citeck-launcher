package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
)

// TestWriteSystemDumpZip_IncludesGoroutineDump asserts the ZIP carries a
// non-empty `goroutine-dump.txt` entry (Kotlin parity with
// SystemDumpUtils.exportThreadDump → `thread-dump.txt`).
func TestWriteSystemDumpZip_IncludesGoroutineDump(t *testing.T) {
	d := &Daemon{}
	rec := httptest.NewRecorder()

	d.writeSystemDumpZip(context.Background(), rec, map[string]any{"daemon": "test"}, nil, nil)

	require.Equal(t, "application/zip", rec.Header().Get("Content-Type"))

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)

	var found *zip.File
	for _, f := range zr.File {
		if f.Name == "goroutine-dump.txt" {
			found = f
			break
		}
	}
	require.NotNil(t, found, "goroutine-dump.txt entry missing from system dump ZIP")

	rc, err := found.Open()
	require.NoError(t, err)
	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.NotEmpty(t, data, "goroutine dump entry should be non-empty")
	// pprof debug=2 starts each goroutine block with `goroutine N [state]:`.
	assert.Contains(t, string(data), "goroutine ",
		"goroutine dump should contain pprof debug=2 header, got: %q", truncate(string(data), 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestHandleSystemDump_DaemonYmlOnlyWhenFileExists: the ZIP must not fabricate
// a daemon.yml out of in-memory defaults — desktop installs never write the
// file, and a synthetic entry misleads whoever reads the dump. When the file
// exists, the effective config is included with api_auth.token masked.
func TestHandleSystemDump_DaemonYmlOnlyWhenFileExists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)

	dumpEntries := func(d *Daemon) map[string]string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/system/dump?format=zip", http.NoBody)
		d.handleSystemDump(rec, req)
		zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
		require.NoError(t, err)
		out := make(map[string]string, len(zr.File))
		for _, f := range zr.File {
			rc, openErr := f.Open()
			require.NoError(t, openErr)
			data, readErr := io.ReadAll(rc)
			_ = rc.Close()
			require.NoError(t, readErr)
			out[f.Name] = string(data)
		}
		return out
	}

	d := &Daemon{daemonCfg: config.DefaultDaemonConfig()}
	d.daemonCfg.APIAuth.Token = "super-secret-token"

	// No conf/daemon.yml on disk (typical desktop) → no entry.
	entries := dumpEntries(d)
	_, has := entries["daemon.yml"]
	assert.False(t, has, "daemon.yml must not be fabricated when the file doesn't exist")

	// File exists (server install) → entry present, token masked.
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "conf"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "conf", "daemon.yml"),
		[]byte("server:\n  webui:\n    enabled: false\n"), 0o600))
	entries = dumpEntries(d)
	content, has := entries["daemon.yml"]
	require.True(t, has, "daemon.yml must be included when the file exists")
	assert.NotContains(t, content, "super-secret-token", "api_auth.token must be masked")
	assert.Contains(t, content, "***REDACTED***")
}
