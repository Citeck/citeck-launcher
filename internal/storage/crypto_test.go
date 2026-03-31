package storage

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := deriveKey("test-password", []byte("test-salt-16byt"), 1000)
	original := "my-secret-token-value"

	encrypted, err := encryptValue(key, []byte(original))
	require.NoError(t, err)
	assert.NotEqual(t, original, encrypted) // base64, not plaintext

	decrypted, err := decryptValue(key, encrypted)
	require.NoError(t, err)
	assert.Equal(t, original, decrypted)
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1 := deriveKey("password-one", []byte("salt-1234567890"), 1000)
	key2 := deriveKey("password-two", []byte("salt-1234567890"), 1000)

	encrypted, err := encryptValue(key1, []byte("secret"))
	require.NoError(t, err)

	_, err = decryptValue(key2, encrypted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt")
}

func TestSetMasterPassword(t *testing.T) {
	store := newTestStore(t)

	// Add plaintext secrets
	for _, s := range []Secret{
		{SecretMeta: SecretMeta{ID: "s1", Name: "Token 1", Type: SecretGitToken}, Value: "glpat-abc123"},
		{SecretMeta: SecretMeta{ID: "s2", Name: "Registry", Type: SecretRegistryAuth}, Value: "admin:pass"},
		{SecretMeta: SecretMeta{ID: "s3", Name: "Basic", Type: SecretBasicAuth}, Value: "user:secret"},
	} {
		require.NoError(t, store.SaveSecret(s))
	}

	svc, err := NewSecretService(store)
	require.NoError(t, err)

	assert.False(t, svc.IsEncrypted())
	assert.False(t, svc.IsLocked())

	// Set master password
	require.NoError(t, svc.SetMasterPassword("my-master-pwd"))

	assert.True(t, svc.IsEncrypted())
	assert.False(t, svc.IsLocked()) // key is still in memory

	// Verify GetSecret returns decrypted values
	sec, err := svc.GetSecret("s1")
	require.NoError(t, err)
	assert.Equal(t, "glpat-abc123", sec.Value)

	sec, err = svc.GetSecret("s2")
	require.NoError(t, err)
	assert.Equal(t, "admin:pass", sec.Value)

	// Verify raw DB values are NOT plaintext
	var rawValue string
	err = store.DB().QueryRow("SELECT value FROM secrets WHERE id = ?", "s1").Scan(&rawValue)
	require.NoError(t, err)
	assert.NotEqual(t, "glpat-abc123", rawValue)
	assert.NotEmpty(t, rawValue)

	// Verify launcher_state has metadata
	enc, err := store.GetStateValue(stateEncrypted)
	require.NoError(t, err)
	assert.Equal(t, "true", enc)

	params, err := store.GetStateValue(stateKeyParams)
	require.NoError(t, err)
	assert.Contains(t, params, "iterations")

	verify, err := store.GetStateValue(stateVerify)
	require.NoError(t, err)
	assert.NotEmpty(t, verify)
}

func TestSetMasterPasswordEmpty(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)

	require.NoError(t, svc.SetMasterPassword("pwd"))

	assert.True(t, svc.IsEncrypted())
	assert.False(t, svc.IsLocked())

	enc, err := store.GetStateValue(stateEncrypted)
	require.NoError(t, err)
	assert.Equal(t, "true", enc)
}

func TestUnlockCorrectPassword(t *testing.T) {
	store := newTestStore(t)

	// Add a secret and encrypt
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "tok", Name: "Token", Type: SecretGitToken},
		Value:      "glpat-xyz789",
	}))
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("unlock-test"))

	// Simulate restart: new SecretService on same DB
	svc2, err := NewSecretService(store)
	require.NoError(t, err)

	assert.True(t, svc2.IsEncrypted())
	assert.True(t, svc2.IsLocked())

	// Unlock with correct password
	require.NoError(t, svc2.Unlock("unlock-test"))

	assert.True(t, svc2.IsEncrypted())
	assert.False(t, svc2.IsLocked())

	sec, err := svc2.GetSecret("tok")
	require.NoError(t, err)
	assert.Equal(t, "glpat-xyz789", sec.Value)
}

func TestUnlockWrongPassword(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("correct"))

	svc2, err := NewSecretService(store)
	require.NoError(t, err)

	err = svc2.Unlock("wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid password")
	assert.True(t, svc2.IsLocked())
}

func TestGetSecretWhileLocked(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "s1", Name: "S1", Type: SecretGitToken},
		Value:      "val",
	}))

	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	// Simulate restart
	svc2, err := NewSecretService(store)
	require.NoError(t, err)
	assert.True(t, svc2.IsLocked())

	_, err = svc2.GetSecret("s1")
	require.ErrorIs(t, err, ErrSecretsLocked)
}

func TestSaveSecretWhileLocked(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	svc2, err := NewSecretService(store)
	require.NoError(t, err)
	assert.True(t, svc2.IsLocked())

	err = svc2.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "new", Name: "New", Type: SecretGitToken},
		Value:      "val",
	})
	require.ErrorIs(t, err, ErrSecretsLocked)
}

func TestSaveSecretEncrypted(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	// Save a new secret through the service
	require.NoError(t, svc.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "new", Name: "New", Type: SecretGitToken},
		Value:      "plaintext-token",
	}))

	// Verify raw DB value is not plaintext
	var rawValue string
	err = store.DB().QueryRow("SELECT value FROM secrets WHERE id = ?", "new").Scan(&rawValue)
	require.NoError(t, err)
	assert.NotEqual(t, "plaintext-token", rawValue)

	// Verify GetSecret returns decrypted
	sec, err := svc.GetSecret("new")
	require.NoError(t, err)
	assert.Equal(t, "plaintext-token", sec.Value)
}

func TestSetMasterPasswordAlreadyEncrypted(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("first"))

	err = svc.SetMasterPassword("second")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already configured")
}

func TestUnlockNotEncrypted(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)

	err = svc.Unlock("pwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not encrypted")
}

func TestListSecretsPassthrough(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "s1", Name: "S1", Type: SecretGitToken},
		Value:      "val",
	}))

	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	// Simulate locked state
	svc2, err := NewSecretService(store)
	require.NoError(t, err)
	assert.True(t, svc2.IsLocked())

	// ListSecrets should work regardless of lock state
	secrets, err := svc2.ListSecrets()
	require.NoError(t, err)
	assert.Len(t, secrets, 1)
	assert.Equal(t, "s1", secrets[0].ID)
}

func TestDeleteSecretPassthrough(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "s1", Name: "S1", Type: SecretGitToken},
		Value:      "val",
	}))

	svc, err := NewSecretService(store)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteSecret("s1"))

	secrets, err := svc.ListSecrets()
	require.NoError(t, err)
	assert.Empty(t, secrets)
}

func TestRawDBQueryForEncryptedSecrets(t *testing.T) {
	// Verify that after encryption, direct SQL queries don't return plaintext
	store := newTestStore(t)
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "tok", Name: "Token", Type: SecretGitToken},
		Value:      "super-secret-token",
	}))

	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	// Raw query should NOT show "super-secret-token"
	rows, err := store.DB().Query("SELECT id, value FROM secrets")
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var id, value string
		require.NoError(t, rows.Scan(&id, &value))
		assert.NotContains(t, value, "super-secret-token")
	}
}

func TestUnlockIdempotent(t *testing.T) {
	store := newTestStore(t)
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd"))

	svc2, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc2.Unlock("pwd"))
	// Second unlock should be idempotent
	require.NoError(t, svc2.Unlock("pwd"))
	assert.False(t, svc2.IsLocked())
}

// --- Helpers ---

func getRawSecretValue(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var val string
	err := db.QueryRow("SELECT value FROM secrets WHERE id = ?", id).Scan(&val)
	require.NoError(t, err)
	return val
}
