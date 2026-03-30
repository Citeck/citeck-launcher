package h2migrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestImportExportJSON_RealData(t *testing.T) {
	homeDir := os.ExpandEnv("$HOME/.citeck/launcher")
	exportPath := filepath.Join(homeDir, "h2-export.json")

	if _, err := os.Stat(exportPath); os.IsNotExist(err) {
		t.Skip("h2-export.json not found — run JAR export first")
	}

	// Use temp dir for SQLite to avoid touching real data
	tmpDir := t.TempDir()
	store, err := storage.NewSQLiteStore(tmpDir)
	require.NoError(t, err)
	defer store.Close()

	result, err := ImportExportJSON(homeDir, exportPath, store)
	require.NoError(t, err)

	t.Logf("Result: workspaces=%d, namespaces=%d, secrets=%d, errors=%d",
		result.Workspaces, result.Namespaces, result.Secrets, result.Errors)

	require.True(t, result.Workspaces > 0, "should import workspaces")
	require.True(t, result.Namespaces > 0, "should import namespaces")
	require.True(t, result.Secrets > 0, "should import secrets")
	require.Zero(t, result.Errors, "should have no errors")

	// Verify workspaces
	ws, err := store.ListWorkspaces()
	require.NoError(t, err)
	require.True(t, len(ws) > 0)
	t.Logf("Workspaces: %d", len(ws))
	for _, w := range ws {
		t.Logf("  %s: name=%s repo=%s", w.ID, w.Name, w.RepoURL)
	}

	// Verify secrets blob
	blob, err := store.GetSecretBlob()
	require.NoError(t, err)
	require.True(t, len(blob) > 0, "secret blob should be non-empty")
	t.Logf("Secret blob: %d bytes", len(blob))

	// Verify namespace.yml files created in tmpDir won't exist (different homeDir)
	// but we can check the real homeDir
	nsConfigPath := filepath.Join(homeDir, "ws", "DEFAULT", "ns", "h6gmfja", "namespace.yml")
	if _, err := os.Stat(nsConfigPath); err == nil {
		data, _ := os.ReadFile(nsConfigPath)
		t.Logf("namespace.yml sample:\n%s", string(data))
	}
}
