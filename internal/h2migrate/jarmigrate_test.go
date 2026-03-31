package h2migrate

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/storage"
)

func TestImportExportJSON_SyntheticData(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	// Build synthetic export JSON matching the h2ExportJSON format
	workspaceData := map[string]any{
		"name":       "Test Workspace",
		"repoUrl":    "https://github.com/test/repo.git",
		"repoBranch": "develop",
	}
	wsJSON, _ := json.Marshal(workspaceData)

	namespaceData := map[string]any{
		"name":      "My Namespace",
		"bundleRef": "community:2025.12",
		"authentication": map[string]any{
			"type": "KEYCLOAK",
		},
	}
	nsJSON, _ := json.Marshal(namespaceData)

	exportData := h2ExportJSON{
		Version: 1,
		Maps: map[string]map[string]string{
			"entities!workspace": {
				"ws-001": base64.StdEncoding.EncodeToString(wsJSON),
			},
			"entities/ws-001!namespace": {
				"ns-abc": base64.StdEncoding.EncodeToString(nsJSON),
			},
			"secrets!data": {
				"storage": "dGVzdC1ibG9i", // encodes "test-blob"
			},
			"launcher!state": {
				"selectedWorkspace": base64.StdEncoding.EncodeToString([]byte(`"ws-001"`)),
			},
			"workspace-state!ws-001": {
				"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-abc"`)),
			},
		},
	}
	exportJSON, err := json.Marshal(exportData)
	require.NoError(t, err)

	exportPath := filepath.Join(homeDir, "test-export.json")
	require.NoError(t, os.WriteFile(exportPath, exportJSON, 0o644))

	result, err := ImportExportJSON(homeDir, exportPath, store)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Workspaces, "should import 1 workspace")
	assert.Equal(t, 1, result.Namespaces, "should import 1 namespace")
	assert.Equal(t, 1, result.Secrets, "should import 1 secret blob")
	assert.Equal(t, 0, result.Errors, "should have no errors")

	// Verify workspace was saved
	ws, err := store.GetWorkspace("ws-001")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "Test Workspace", ws.Name)
	assert.Equal(t, "https://github.com/test/repo.git", ws.RepoURL)
	assert.Equal(t, "develop", ws.RepoBranch)

	// Verify namespace.yml was created
	nsConfigPath := filepath.Join(homeDir, "ws", "ws-001", "ns", "ns-abc", "namespace.yml")
	_, err = os.Stat(nsConfigPath)
	require.NoError(t, err, "namespace.yml should exist")
	nsContent, err := os.ReadFile(nsConfigPath)
	require.NoError(t, err)
	nsStr := string(nsContent)
	assert.Contains(t, nsStr, "ns-abc")
	assert.Contains(t, nsStr, "My Namespace")
	assert.Contains(t, nsStr, "community:2025.12")

	// Verify secrets blob was stored
	blob, err := store.GetSecretBlob()
	require.NoError(t, err)
	assert.Equal(t, "dGVzdC1ibG9i", blob)

	// Verify launcher state
	state, err := store.GetState()
	require.NoError(t, err)
	assert.Equal(t, "ws-001", state.WorkspaceID)
	assert.Equal(t, "ns-abc", state.NamespaceID)
}

func TestImportExportJSON_UnsupportedVersion(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	exportData := h2ExportJSON{
		Version: 99,
		Maps:    map[string]map[string]string{},
	}
	exportJSON, _ := json.Marshal(exportData)
	exportPath := filepath.Join(homeDir, "bad-version.json")
	require.NoError(t, os.WriteFile(exportPath, exportJSON, 0o644))

	_, err = ImportExportJSON(homeDir, exportPath, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported export version")
}

func TestImportExportJSON_EmptyMaps(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	exportData := h2ExportJSON{
		Version: 1,
		Maps:    map[string]map[string]string{},
	}
	exportJSON, _ := json.Marshal(exportData)
	exportPath := filepath.Join(homeDir, "empty.json")
	require.NoError(t, os.WriteFile(exportPath, exportJSON, 0o644))

	result, err := ImportExportJSON(homeDir, exportPath, store)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Workspaces)
	assert.Equal(t, 0, result.Namespaces)
	assert.Equal(t, 0, result.Secrets)
}

func TestImportExportJSON_MissingFile(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	_, err = ImportExportJSON(homeDir, filepath.Join(homeDir, "nonexistent.json"), store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read export file")
}

func TestImportExportJSON_WorkspaceDefaultsNameToID(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	// Workspace with no name field — should default to the map key (ID)
	wsData := map[string]any{"repoUrl": "https://example.com/repo.git"}
	wsJSON, _ := json.Marshal(wsData)

	exportData := h2ExportJSON{
		Version: 1,
		Maps: map[string]map[string]string{
			"entities!workspace": {
				"DEFAULT": base64.StdEncoding.EncodeToString(wsJSON),
			},
		},
	}
	exportJSON, _ := json.Marshal(exportData)
	exportPath := filepath.Join(homeDir, "export.json")
	require.NoError(t, os.WriteFile(exportPath, exportJSON, 0o644))

	result, err := ImportExportJSON(homeDir, exportPath, store)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Workspaces)

	ws, err := store.GetWorkspace("DEFAULT")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "DEFAULT", ws.Name, "name should default to ID")
}

func TestImportExportJSON_NamespaceSkipsVersionsAndRuntime(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	nsData := map[string]any{"name": "ns1", "bundleRef": "community:2025.12"}
	nsJSON, _ := json.Marshal(nsData)

	exportData := h2ExportJSON{
		Version: 1,
		Maps: map[string]map[string]string{
			// These should be skipped (contain "versions" or "runtime")
			"entities/ws1/versions!namespace": {
				"ns1": base64.StdEncoding.EncodeToString(nsJSON),
			},
			"entities/runtime!namespace": {
				"ns1": base64.StdEncoding.EncodeToString(nsJSON),
			},
			// This is the real one
			"entities/ws1!namespace": {
				"ns1": base64.StdEncoding.EncodeToString(nsJSON),
			},
		},
	}
	exportJSON, _ := json.Marshal(exportData)
	exportPath := filepath.Join(homeDir, "export.json")
	require.NoError(t, os.WriteFile(exportPath, exportJSON, 0o644))

	result, err := ImportExportJSON(homeDir, exportPath, store)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Namespaces, "should only import the real namespace, not versions/runtime")
}

func TestCheckMigration_NoH2File(t *testing.T) {
	homeDir := t.TempDir()
	status := CheckMigration(homeDir)
	assert.False(t, status.Needed, "no storage.db means no migration needed")
}

func TestCheckMigration_BothExist(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "launcher.db"), []byte("sqlite"), 0o644))

	status := CheckMigration(homeDir)
	assert.False(t, status.Needed, "both files exist means migration already done")
}

func TestCheckMigration_OnlyH2Exists(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))

	status := CheckMigration(homeDir)
	assert.True(t, status.Needed, "only H2 exists means migration is needed")
}

func TestNeedsMigration_OnlyH2(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.True(t, needed)
}

func TestNeedsMigration_NeitherFile(t *testing.T) {
	homeDir := t.TempDir()

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.False(t, needed)
}

func TestNeedsMigration_BothFiles(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "launcher.db"), []byte("sqlite"), 0o644))

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.False(t, needed)
}

func TestFindJava_NotInPath(t *testing.T) {
	// Override PATH to empty dir to ensure java is not found via LookPath
	t.Setenv("PATH", t.TempDir())
	t.Setenv("JAVA_HOME", "")

	result := findJava()

	// findJava checks hardcoded paths like /usr/bin/java as fallback.
	// If the system has java installed, the function will find it there.
	// We can only assert that the result is either empty (no java anywhere)
	// or a valid path that exists.
	if result != "" {
		_, err := os.Stat(result)
		assert.NoError(t, err, "findJava returned a path that does not exist: %s", result)
	}
}

func TestFindJava_UsesJAVA_HOME(t *testing.T) {
	javaHome := t.TempDir()
	binDir := filepath.Join(javaHome, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	javaPath := filepath.Join(binDir, "java")
	require.NoError(t, os.WriteFile(javaPath, []byte("#!/bin/sh"), 0o755))

	t.Setenv("JAVA_HOME", javaHome)
	t.Setenv("PATH", t.TempDir()) // empty PATH

	result := findJava()
	assert.Equal(t, javaPath, result, "findJava should find java from JAVA_HOME")
}
