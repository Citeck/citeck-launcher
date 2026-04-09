package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryDir(t *testing.T) {
	assert.Equal(t, "/conf/namespace_history", historyDir("/conf/namespace.yml"))
	assert.Equal(t, "/conf/daemon_history", historyDir("/conf/daemon.yml"))
	assert.Equal(t, "/a/b/myconfig_history", historyDir("/a/b/myconfig.yaml"))
}

func TestPatchFileName(t *testing.T) {
	ts := time.Date(2026, 4, 8, 12, 30, 0, 123000000, time.UTC)
	name := patchFileName(ts, "hostname")
	assert.Equal(t, "2026-04-08T12-30-00.123_hostname.json", name)
}

func TestWriteAndReadPatch(t *testing.T) {
	dir := t.TempDir()
	patch := &PatchRecord{
		Date:    time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC),
		Setting: "hostname",
		Command: "citeck setup hostname",
		Input:   map[string]any{"host": "new.com"},
		Forward: []PatchOp{{Op: "replace", Path: "/proxy/host", Value: "new.com"}},
		Reverse: []PatchOp{{Op: "replace", Path: "/proxy/host", Value: "old.com"}},
	}

	path, err := writePatch(dir, patch)
	require.NoError(t, err)
	assert.FileExists(t, path)

	loaded, err := readPatch(path)
	require.NoError(t, err)
	assert.Equal(t, "hostname", loaded.Setting)
	assert.Equal(t, "citeck setup hostname", loaded.Command)
	require.Len(t, loaded.Forward, 1)
	assert.Equal(t, "/proxy/host", loaded.Forward[0].Path)
}

func TestWriteAndReadSnapshot(t *testing.T) {
	dir := t.TempDir()
	content := []byte("apiVersion: v1\nid: test\n")

	err := writeSnapshot(dir, content)
	require.NoError(t, err)

	data, err := readSnapshot(dir)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestReadSnapshot_NotExists(t *testing.T) {
	dir := t.TempDir()
	data, err := readSnapshot(dir)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestListPatches(t *testing.T) {
	dir := t.TempDir()
	p1 := &PatchRecord{Date: time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC), Setting: "hostname"}
	p2 := &PatchRecord{Date: time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC), Setting: "tls"}
	_, err := writePatch(dir, p1)
	require.NoError(t, err)
	_, err = writePatch(dir, p2)
	require.NoError(t, err)

	patches, err := listPatches(dir)
	require.NoError(t, err)
	require.Len(t, patches, 2)
	assert.Equal(t, "hostname", patches[0].Setting)
	assert.Equal(t, "tls", patches[1].Setting)
}

func TestListPatches_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	patches, err := listPatches(dir)
	require.NoError(t, err)
	assert.Empty(t, patches)
}

func TestListPatches_NoDir(t *testing.T) {
	patches, err := listPatches(filepath.Join(t.TempDir(), "nonexistent"))
	require.NoError(t, err)
	assert.Empty(t, patches)
}

func TestBridgeCheck_FirstRun(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "namespace_history")

	currentData := []byte(`{"id":"test","proxy":{"host":"a.com"}}`)
	bridgeNeeded, err := checkBridge(histDir, currentData)
	require.NoError(t, err)
	assert.False(t, bridgeNeeded)

	snap, err := readSnapshot(histDir)
	require.NoError(t, err)
	assert.Equal(t, currentData, snap)
}

func TestBridgeCheck_ExternalChange(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "namespace_history")

	oldData := []byte(`{"id":"test","proxy":{"host":"a.com"}}`)
	_, err := checkBridge(histDir, oldData)
	require.NoError(t, err)

	newData := []byte(`{"id":"test","proxy":{"host":"b.com"}}`)
	bridgeNeeded, err := checkBridge(histDir, newData)
	require.NoError(t, err)
	assert.True(t, bridgeNeeded)

	patches, err := listPatches(histDir)
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "external_change", patches[0].Setting)

	snap, err := readSnapshot(histDir)
	require.NoError(t, err)
	assert.Equal(t, newData, snap)
}

func TestBridgeCheck_NoChange(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "namespace_history")

	data := []byte(`{"id":"test","proxy":{"host":"a.com"}}`)
	_, _ = checkBridge(histDir, data)

	bridgeNeeded, err := checkBridge(histDir, data)
	require.NoError(t, err)
	assert.False(t, bridgeNeeded)
}

func TestListPatches_IgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()
	// Write a non-.json file — should be ignored
	require.NoError(t, os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("data"), 0o644))

	patches, err := listPatches(dir)
	require.NoError(t, err)
	assert.Empty(t, patches)
}
