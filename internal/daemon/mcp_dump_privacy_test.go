package daemon

import (
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemDumpOmitsMCPDatabaseSidecarsAndLegacyBackup(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	dir := filepath.Join(config.ConfDir(), "mcp-private")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for _, name := range []string{"mcp.db", "mcp.db-wal", "mcp.db-shm", "mcp.db-journal", "legacy.backup", "state.json", "owner.lock", "fence-interrupted.tmp"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("private-mcp-authority-marker"), 0o600))
	}
	entries := dumpZipEntries(t, &Daemon{})
	require.Contains(t, entries, "system-info.json")
	for name, data := range entries {
		require.NotContains(t, name, "mcp-private")
		require.NotContains(t, data, "private-mcp-authority-marker")
	}
}
