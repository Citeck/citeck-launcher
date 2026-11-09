package jvmattach

import (
	"os"
	"runtime"
	"testing"
)

// Use a short, private socket directory, independent of TMPDIR and test names.
// macOS limits Unix socket paths to about 104 bytes. Cleanup runs after the
// fixture listener has closed; no shared socket name or caller directory is used.
func attachSocketDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "attach-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
