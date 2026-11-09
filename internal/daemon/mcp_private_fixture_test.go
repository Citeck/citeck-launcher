package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Match the production private-directory boundary independently of TempDir's
// platform/umask-dependent mode. Do not chmod the entire temporary ancestor.
func privateMCPStorePath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mcp-private")
	require.NoError(t, os.Mkdir(dir, 0o700))
	return filepath.Join(dir, "state.json")
}
