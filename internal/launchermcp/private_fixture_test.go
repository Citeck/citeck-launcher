package launchermcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TempDir is an ancestor, not the private database directory. Its permissions
// depend on the toolchain/umask; never weaken production's owner-only check just
// to make a fixture work. A fresh child has an explicit private creation mode.
func testPrivateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.Mkdir(dir, 0o700))
	return dir
}

func testStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(testPrivateDir(t), "state.json")
}
