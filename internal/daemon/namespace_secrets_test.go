package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newNsSecretService(t *testing.T) (*storage.SecretService, storage.Store) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	return svc, store
}

func wsSecrets(kv ...string) *bundle.WorkspaceConfig {
	ws := &bundle.WorkspaceConfig{}
	for i := 0; i+1 < len(kv); i += 2 {
		ws.Secrets = append(ws.Secrets, bundle.SecretDefault{ID: kv[i], Value: kv[i+1]})
	}
	return ws
}

// The workspace value is a DEFAULT: the namespace stores its own copy once and
// keeps it through a later edit of the default and through its removal — a
// password is baked into the database's volume on first start.
func TestANamespaceKeepsItsOwnSecretValue(t *testing.T) {
	svc, _ := newNsSecretService(t)

	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("db", "first"), true)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"db": "first"}, got)

	got, err = loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("db", "changed"), true)
	require.NoError(t, err)
	assert.Equal(t, "first", got["db"], "an edited default does not reach an existing namespace")

	got, err = loadNamespaceSecrets(svc, "ws", "ns", wsSecrets(), true)
	require.NoError(t, err)
	assert.Equal(t, "first", got["db"], "nor does removing it from the workspace")

	other, err := loadNamespaceSecrets(svc, "ws", "other-ns", wsSecrets("db", "changed"), true)
	require.NoError(t, err)
	assert.Equal(t, "changed", other["db"], "each namespace has its own copy")

	sameNsOtherWs, err := loadNamespaceSecrets(svc, "ws2", "ns", wsSecrets("db", "ws2"), true)
	require.NoError(t, err)
	assert.Equal(t, "ws2", sameNsOtherWs["db"], "a namespace id is unique only within its workspace")
}

// A reload preview answers what seeding WOULD store, and stores nothing.
func TestAPreviewDoesNotStoreSecrets(t *testing.T) {
	svc, _ := newNsSecretService(t)
	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("db", "v"), false)
	require.NoError(t, err)
	assert.Equal(t, "v", got["db"])

	metas, err := svc.ListSecrets()
	require.NoError(t, err)
	assert.Empty(t, metas)
}

// Stored values are secrets like any other: with a master password they are
// encrypted, and a vault waiting for its password cannot be read — the caller
// then generates without them.
func TestNamespaceSecretsFollowTheVault(t *testing.T) {
	svc, store := newNsSecretService(t)
	_, err := loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("db", "observer"), true)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd", false))

	locked, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, locked.IsLocked())
	_, err = loadNamespaceSecrets(locked, "ws", "ns", wsSecrets("db", "observer"), true)
	require.ErrorIs(t, err, storage.ErrSecretsLocked)
	assert.Nil(t, namespaceSecretsFor(locked, "ws", "ns", wsSecrets("db", "observer"), true))

	require.NoError(t, locked.Unlock("pwd"))
	got, err := loadNamespaceSecrets(locked, "ws", "ns", nil, true)
	require.NoError(t, err)
	assert.Equal(t, "observer", got["db"])
}

func TestDeletingANamespaceDeletesItsSecrets(t *testing.T) {
	svc, _ := newNsSecretService(t)
	_, err := loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("a", "1", "b", "2"), true)
	require.NoError(t, err)
	_, err = loadNamespaceSecrets(svc, "ws", "keep", wsSecrets("a", "1"), true)
	require.NoError(t, err)

	deleteNamespaceSecrets(svc, "ws", "ns")

	metas, err := svc.ListSecrets()
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, namespaceSecretScope("ws", "keep"), metas[0].Scope)
}
