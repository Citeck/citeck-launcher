package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestSQLiteRegistryBindingRoundTrip pins the per-workspace host→secret
// binding contract: upsert, list (workspace-scoped), update, and delete (empty
// secret id).
func TestSQLiteRegistryBindingRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.SetRegistryBinding("ws-a", "harbor.citeck.ru", "sec1"))
	require.NoError(t, store.SetRegistryBinding("ws-a", "enterprise-registry.citeck.ru", "sec2"))
	require.NoError(t, store.SetRegistryBinding("ws-b", "harbor.citeck.ru", "sec3"))

	a, err := store.ListRegistryBindings("ws-a")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"harbor.citeck.ru":              "sec1",
		"enterprise-registry.citeck.ru": "sec2",
	}, a)

	// Bindings are workspace-scoped — ws-b sees only its own.
	b, err := store.ListRegistryBindings("ws-b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"harbor.citeck.ru": "sec3"}, b)

	// Upsert replaces the secret id for the same (ws, host).
	require.NoError(t, store.SetRegistryBinding("ws-a", "harbor.citeck.ru", "sec9"))
	a2, _ := store.ListRegistryBindings("ws-a")
	assert.Equal(t, "sec9", a2["harbor.citeck.ru"])

	// Empty secret id removes the binding.
	require.NoError(t, store.SetRegistryBinding("ws-a", "harbor.citeck.ru", ""))
	a3, _ := store.ListRegistryBindings("ws-a")
	_, present := a3["harbor.citeck.ru"]
	assert.False(t, present)
	assert.Equal(t, "sec2", a3["enterprise-registry.citeck.ru"], "other host's binding untouched")
}

// TestFileStoreRegistryBindingRoundTrip mirrors the contract for server mode,
// where bindings are stored flat (single implicit workspace).
func TestFileStoreRegistryBindingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, filepath.Join(dir, "runtime"))
	require.NoError(t, err)
	defer store.Close()

	// Empty before anything is set.
	empty, err := store.ListRegistryBindings("")
	require.NoError(t, err)
	assert.Empty(t, empty)

	require.NoError(t, store.SetRegistryBinding("", "harbor.citeck.ru", "s1"))
	require.NoError(t, store.SetRegistryBinding("", "enterprise-registry.citeck.ru", "s2"))
	got, err := store.ListRegistryBindings("")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"harbor.citeck.ru":              "s1",
		"enterprise-registry.citeck.ru": "s2",
	}, got)

	// Delete via empty id.
	require.NoError(t, store.SetRegistryBinding("", "harbor.citeck.ru", ""))
	got2, _ := store.ListRegistryBindings("")
	assert.Equal(t, map[string]string{"enterprise-registry.citeck.ru": "s2"}, got2)
}
