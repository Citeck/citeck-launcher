package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestNamespaceYml writes a minimal valid namespace.yml with the given
// proxy host and returns its path.
func writeTestNamespaceYml(t *testing.T, dir, host string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "namespace.yml")
	data := fmt.Sprintf("id: default\nproxy:\n  host: %s\n  port: 80\n", host)
	require.NoError(t, os.WriteFile(cfgPath, []byte(data), 0o600))
	return cfgPath
}

// hostnamePatchRecord returns a record describing a /proxy/host change
// oldHost → newHost.
func hostnamePatchRecord(oldHost, newHost string) *PatchRecord {
	return &PatchRecord{
		Date:    time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		Setting: "hostname",
		Command: "setup hostname",
		Forward: []PatchOp{{Op: "replace", Path: "/proxy/host", Value: newHost}},
		Reverse: []PatchOp{{Op: "replace", Path: "/proxy/host", Value: oldHost}},
	}
}

func TestPerformUndo_AppliesReverseOps(t *testing.T) {
	dir := t.TempDir()
	// Current state: the forward patch was applied (host = new.com).
	cfgPath := writeTestNamespaceYml(t, dir, "new.com")
	histDir := historyDir(cfgPath)
	rec := hostnamePatchRecord("old.com", "new.com")

	newID, err := performUndo(NamespaceFile, cfgPath, histDir, "entry-1", rec)
	require.NoError(t, err)
	assert.NotEmpty(t, newID)

	// Config reverted.
	cfg, err := namespace.LoadNamespaceConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "old.com", cfg.Proxy.Host)
}

func TestPerformUndo_RecordsHistoryEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestNamespaceYml(t, dir, "new.com")
	histDir := historyDir(cfgPath)
	rec := hostnamePatchRecord("old.com", "new.com")

	newID, err := performUndo(NamespaceFile, cfgPath, histDir, "entry-1", rec)
	require.NoError(t, err)

	entries, err := listPatchEntries(histDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	undoRec := entries[0]
	assert.Equal(t, newID, undoRec.Name)
	assert.Equal(t, "undo_hostname", undoRec.Record.Setting)
	assert.Equal(t, "setup history --undo entry-1", undoRec.Record.Command)
	// Forward of the undo = reverse of the original (and vice versa),
	// so the undo is itself undoable.
	require.Len(t, undoRec.Record.Forward, 1)
	assert.Equal(t, "replace", undoRec.Record.Forward[0].Op)
	assert.Equal(t, "/proxy/host", undoRec.Record.Forward[0].Path)
	assert.Equal(t, "old.com", undoRec.Record.Forward[0].Value)
	require.Len(t, undoRec.Record.Reverse, 1)
	assert.Equal(t, "new.com", undoRec.Record.Reverse[0].Value)

	// Snapshot updated to the post-undo state (no bridge on next change).
	snap, err := readSnapshot(histDir)
	require.NoError(t, err)
	require.NotNil(t, snap)
	bridged, err := checkBridge(histDir, snap)
	require.NoError(t, err)
	assert.False(t, bridged)
}

func TestPerformUndo_UndoOfUndo_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestNamespaceYml(t, dir, "new.com")
	histDir := historyDir(cfgPath)
	rec := hostnamePatchRecord("old.com", "new.com")

	// First undo: new.com → old.com.
	firstID, err := performUndo(NamespaceFile, cfgPath, histDir, "entry-1", rec)
	require.NoError(t, err)

	entries, err := listPatchEntries(histDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Undo the undo (reading the record back from disk, as the CLI does):
	// old.com → new.com.
	secondID, err := performUndo(NamespaceFile, cfgPath, histDir, firstID, entries[0].Record)
	require.NoError(t, err)
	assert.NotEqual(t, firstID, secondID)

	cfg, err := namespace.LoadNamespaceConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "new.com", cfg.Proxy.Host)

	// Both undos recorded.
	entries, err = listPatchEntries(histDir)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestPerformUndo_StaleReverse_Refuses(t *testing.T) {
	dir := t.TempDir()
	// The host was changed again after the entry was recorded — the reverse
	// no longer applies cleanly.
	cfgPath := writeTestNamespaceYml(t, dir, "other.com")
	histDir := historyDir(cfgPath)
	rec := hostnamePatchRecord("old.com", "new.com")

	_, err := performUndo(NamespaceFile, cfgPath, histDir, "entry-1", rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/proxy/host")

	// Nothing was written: config unchanged, no history entry, no snapshot.
	cfg, lErr := namespace.LoadNamespaceConfig(cfgPath)
	require.NoError(t, lErr)
	assert.Equal(t, "other.com", cfg.Proxy.Host)

	entries, lErr := listPatchEntries(histDir)
	require.NoError(t, lErr)
	assert.Empty(t, entries)
}

func TestPerformUndo_ValidationFailure_Refuses(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTestNamespaceYml(t, dir, "new.com")
	histDir := historyDir(cfgPath)
	// Reverse sets an invalid port — validation must reject before writing.
	rec := &PatchRecord{
		Date:    time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		Setting: "port",
		Forward: []PatchOp{{Op: "replace", Path: "/proxy/port", Value: float64(80)}},
		Reverse: []PatchOp{{Op: "replace", Path: "/proxy/port", Value: float64(0)}},
	}

	_, err := performUndo(NamespaceFile, cfgPath, histDir, "entry-1", rec)
	require.Error(t, err)

	cfg, lErr := namespace.LoadNamespaceConfig(cfgPath)
	require.NoError(t, lErr)
	assert.Equal(t, 80, cfg.Proxy.Port)
}

func TestCheckReverseApplies_PathMissing(t *testing.T) {
	cur := map[string]any{"proxy": map[string]any{"host": "a.com"}}
	rec := &PatchRecord{
		Forward: []PatchOp{{Op: "replace", Path: "/email/host", Value: "smtp.com"}},
		Reverse: []PatchOp{{Op: "replace", Path: "/email/host", Value: "old-smtp.com"}},
	}
	err := checkReverseApplies(cur, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/email/host")
}

func TestCheckReverseApplies_ReAddedWithDifferentValue(t *testing.T) {
	// Forward removed /email; reverse re-adds it. But /email has since been
	// re-created with a different value — refuse.
	cur := map[string]any{"email": map[string]any{"host": "another.com"}}
	rec := &PatchRecord{
		Forward: []PatchOp{{Op: "remove", Path: "/email"}},
		Reverse: []PatchOp{{Op: "add", Path: "/email", Value: map[string]any{"host": "smtp.com"}}},
	}
	err := checkReverseApplies(cur, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/email")
}

func TestCheckReverseApplies_CleanState(t *testing.T) {
	cur := map[string]any{"proxy": map[string]any{"host": "new.com"}}
	rec := hostnamePatchRecord("old.com", "new.com")
	require.NoError(t, checkReverseApplies(cur, rec))
}

func TestFindHistoryEntry(t *testing.T) {
	patches := []indexedPatch{
		{FileName: "2026-06-01T10-00-00.000_hostname", Record: hostnamePatchRecord("a", "b"), Target: NamespaceFile},
	}

	// Exact match.
	e, err := findHistoryEntry(patches, "2026-06-01T10-00-00.000_hostname")
	require.NoError(t, err)
	assert.Equal(t, "hostname", e.Record.Setting)

	// ".json" suffix tolerated.
	e, err = findHistoryEntry(patches, "2026-06-01T10-00-00.000_hostname.json")
	require.NoError(t, err)
	assert.Equal(t, "hostname", e.Record.Setting)

	// Unknown ID.
	_, err = findHistoryEntry(patches, "nope")
	require.Error(t, err)
}
