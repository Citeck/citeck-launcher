package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
