package cli

import (
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpFileCollectorsOmitMCPDatabaseAndBackup(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	dir := filepath.Join(config.ConfDir(), "mcp-private")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for _, name := range []string{"mcp.db", "mcp.db-wal", "mcp.db-shm", "mcp.db-journal", "legacy.backup", "state.json", "owner.lock"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("private-mcp-authority-marker"), 0o600))
	}
	entries := collectedEntries(t, func(dw *dumpWriter) {
		collectConfigView(dw, "citeck/config-view.yaml")
		collectFile(dw, "daemon/daemon.yml", config.DaemonConfigPath())
		collectFile(dw, "daemon/namespace.yml", config.NamespaceConfigPath())
		collectReports(dw)
		collectDesktopUpdateArtifacts(dw, false)
	})
	for name, data := range entries {
		require.NotContains(t, name, "mcp-private")
		require.NotContains(t, data, "private-mcp-authority-marker")
	}
}
