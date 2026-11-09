package cli

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Unix socket names are limited to about 104 bytes on macOS. Neither a test
// name nor a long TMPDIR may consume that budget. MkdirTemp still gives each
// test a fresh owner-only directory; cleanup runs after its listener closes.
func cliSocketDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "cli-sock-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
