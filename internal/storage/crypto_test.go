package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

// --- Crypto primitives (store-independent) ---

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := deriveKey("test-password", []byte("test-salt-16byt"), 1000)
	original := "my-secret-token-value"

	encrypted, err := encryptValue(key, []byte(original))
	require.NoError(t, err)
	assert.NotEqual(t, original, encrypted)

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

// --- SQLite-specific: raw DB query verification (cannot be done via Store interface) ---

func TestRawDBEncryptedValues(t *testing.T) {
	store := newTestSQLiteStore(t)
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "tok", Name: "Token", Type: SecretGitToken},
		Value:      "super-secret-token",
	}))

	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("pwd", false))

	// Raw SQL query should NOT return plaintext
	rows, err := store.DB().Query("SELECT id, value FROM secrets")
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var id, value string
		require.NoError(t, rows.Scan(&id, &value))
		assert.NotContains(t, value, "super-secret-token")
	}

	// SaveSecret through service should also encrypt in DB
	require.NoError(t, svc.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "new", Name: "New", Type: SecretGitToken},
		Value:      "plaintext-token",
	}))
	var rawValue string
	err = store.DB().QueryRow("SELECT value FROM secrets WHERE id = ?", "new").Scan(&rawValue)
	require.NoError(t, err)
	assert.NotEqual(t, "plaintext-token", rawValue)
}

// --- Table-driven tests for both store types ---

func TestSecretServiceBothStores(t *testing.T) {
	stores := map[string]func(t *testing.T) Store{
		"SQLiteStore": func(t *testing.T) Store { return newTestSQLiteStore(t) },
		"FileStore":   func(t *testing.T) Store { return newTestFileStore(t) },
	}

	for storeName, newStore := range stores {
		t.Run(storeName, func(t *testing.T) {
			t.Run("SetMasterPassword+Unlock", func(t *testing.T) {
				store := newStore(t)
				require.NoError(t, store.SaveSecret(Secret{
					SecretMeta: SecretMeta{ID: "tok", Name: "Token", Type: SecretGitToken},
					Value:      "secret-value-123",
				}))

				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("test-pwd", false))

				assert.True(t, svc.IsEncrypted())
				assert.False(t, svc.IsLocked())

				// Service returns decrypted value
				sec, err := svc.GetSecret("tok")
				require.NoError(t, err)
				assert.Equal(t, "secret-value-123", sec.Value)

				// Raw store returns encrypted value
				rawSec, err := store.GetSecret("tok")
				require.NoError(t, err)
				assert.NotEqual(t, "secret-value-123", rawSec.Value)

				// New service starts locked, unlock restores access
				svc2, err := NewSecretService(store)
				require.NoError(t, err)
				assert.True(t, svc2.IsLocked())
				require.NoError(t, svc2.Unlock("test-pwd"))
				assert.False(t, svc2.IsLocked())

				sec, err = svc2.GetSecret("tok")
				require.NoError(t, err)
				assert.Equal(t, "secret-value-123", sec.Value)
			})

			t.Run("WrongPassword", func(t *testing.T) {
				store := newStore(t)
				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("correct", false))

				svc2, err := NewSecretService(store)
				require.NoError(t, err)
				err = svc2.Unlock("wrong")
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid password")
				assert.True(t, svc2.IsLocked())
			})

			t.Run("LockedState", func(t *testing.T) {
				store := newStore(t)
				require.NoError(t, store.SaveSecret(Secret{
					SecretMeta: SecretMeta{ID: "s1", Name: "S1", Type: SecretGitToken},
					Value:      "val",
				}))

				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("pwd", false))

				// New service = locked
				svc2, err := NewSecretService(store)
				require.NoError(t, err)

				// GetSecret returns ErrSecretsLocked
				_, err = svc2.GetSecret("s1")
				require.ErrorIs(t, err, ErrSecretsLocked)

				// SaveSecret returns ErrSecretsLocked
				err = svc2.SaveSecret(Secret{
					SecretMeta: SecretMeta{ID: "new", Name: "New", Type: SecretGitToken},
					Value:      "val",
				})
				require.ErrorIs(t, err, ErrSecretsLocked)

				// ListSecrets works regardless of lock state (metadata only)
				secrets, err := svc2.ListSecrets()
				require.NoError(t, err)
				assert.Len(t, secrets, 1)
			})

			t.Run("AlreadyEncrypted", func(t *testing.T) {
				store := newStore(t)
				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("first", false))

				err = svc.SetMasterPassword("second", false)
				require.ErrorIs(t, err, ErrAlreadyEncrypted)
			})

			t.Run("UnlockNotEncrypted", func(t *testing.T) {
				store := newStore(t)
				svc, err := NewSecretService(store)
				require.NoError(t, err)

				err = svc.Unlock("pwd")
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not encrypted")
			})

			t.Run("UnlockIdempotent", func(t *testing.T) {
				store := newStore(t)
				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("pwd", false))

				svc2, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc2.Unlock("pwd"))
				require.NoError(t, svc2.Unlock("pwd"))
				assert.False(t, svc2.IsLocked())
			})

			t.Run("DefaultPasswordFlag", func(t *testing.T) {
				store := newStore(t)
				svc, err := NewSecretService(store)
				require.NoError(t, err)

				assert.False(t, svc.IsDefaultPassword())

				require.NoError(t, svc.SetMasterPassword("citeck", true))
				assert.True(t, svc.IsDefaultPassword())

				// Persists across restart
				svc2, err := NewSecretService(store)
				require.NoError(t, err)
				assert.True(t, svc2.IsDefaultPassword())
			})

			t.Run("ResetSecrets", func(t *testing.T) {
				store := newStore(t)
				require.NoError(t, store.SaveSecret(Secret{
					SecretMeta: SecretMeta{ID: "s1", Name: "S1", Type: SecretGitToken},
					Value:      "val1",
				}))

				svc, err := NewSecretService(store)
				require.NoError(t, err)
				require.NoError(t, svc.SetMasterPassword("pwd", true))

				assert.True(t, svc.IsEncrypted())
				assert.True(t, svc.IsDefaultPassword())

				require.NoError(t, svc.ResetSecrets())

				assert.False(t, svc.IsEncrypted())
				assert.False(t, svc.IsLocked())
				assert.False(t, svc.IsDefaultPassword())

				// Secrets deleted
				secrets, err := store.ListSecrets()
				require.NoError(t, err)
				assert.Empty(t, secrets)

				// Metadata cleared — key must not exist
				enc, err := store.GetStateValue(stateEncrypted)
				require.NoError(t, err)
				assert.Empty(t, enc)

				// Can set new password after reset
				require.NoError(t, svc.SetMasterPassword("new-pwd", false))
				assert.True(t, svc.IsEncrypted())
				assert.False(t, svc.IsDefaultPassword())
			})

			t.Run("DeleteSecretPassthrough", func(t *testing.T) {
				store := newStore(t)
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
			})
		})
	}
}

// --- GetStateValue/SetStateValue tests for both stores ---

func TestStateValueBothStores(t *testing.T) {
	stores := map[string]func(t *testing.T) Store{
		"SQLiteStore": func(t *testing.T) Store { return newTestSQLiteStore(t) },
		"FileStore":   func(t *testing.T) Store { return newTestFileStore(t) },
	}

	for storeName, newStore := range stores {
		t.Run(storeName, func(t *testing.T) {
			store := newStore(t)

			// Missing key returns ""
			val, err := store.GetStateValue("missing")
			require.NoError(t, err)
			assert.Empty(t, val)

			// Set and get
			require.NoError(t, store.SetStateValue("key1", "value1"))
			val, err = store.GetStateValue("key1")
			require.NoError(t, err)
			assert.Equal(t, "value1", val)

			// Overwrite
			require.NoError(t, store.SetStateValue("key1", "value2"))
			val, err = store.GetStateValue("key1")
			require.NoError(t, err)
			assert.Equal(t, "value2", val)

			// Multiple keys
			require.NoError(t, store.SetStateValue("key2", "other"))
			val, err = store.GetStateValue("key1")
			require.NoError(t, err)
			assert.Equal(t, "value2", val)
			val, err = store.GetStateValue("key2")
			require.NoError(t, err)
			assert.Equal(t, "other", val)

			// Delete via empty value
			require.NoError(t, store.SetStateValue("key1", ""))
			val, err = store.GetStateValue("key1")
			require.NoError(t, err)
			assert.Empty(t, val)
		})
	}
}
