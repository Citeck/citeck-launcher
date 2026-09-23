package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A namespace's own infrastructure secret (a declared database's password) is
// stored even before a master password exists: a desktop stand must come up on
// first start without asking for one. Until then it is plain — the same state
// the vault's own rows are in before encryption is set up — and the moment a
// master password is set, SetMasterPassword encrypts it along with the rest.
// A user secret (a registry token) keeps the old rule and is refused.
func TestANamespaceSecretIsStoredPlainUntilAMasterPasswordExists(t *testing.T) {
	for name, newStore := range map[string]func(t *testing.T) Store{
		"sqlite": func(t *testing.T) Store { return newTestSQLiteStore(t) },
		"file": func(t *testing.T) Store {
			fs, err := NewFileStore(t.TempDir(), t.TempDir())
			require.NoError(t, err)
			return fs
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			svc, err := NewSecretService(store)
			require.NoError(t, err)
			require.False(t, svc.IsEncrypted())

			ns := Secret{SecretMeta: SecretMeta{ID: "ns:n1:db", Name: "db", Type: SecretNamespace, Scope: "ns:n1"}, Value: "observer"}
			require.NoError(t, svc.SaveSecret(ns))
			raw, err := store.GetSecret("ns:n1:db")
			require.NoError(t, err)
			assert.Equal(t, "observer", raw.Value, "no master password yet: stored as is")

			user := Secret{SecretMeta: SecretMeta{ID: "tok", Name: "tok", Type: SecretRegistryAuth}, Value: "t"}
			require.ErrorIs(t, svc.SaveSecret(user), ErrEncryptionNotSetUp, "user secrets keep the old rule")

			require.NoError(t, svc.SetMasterPassword("pwd", false))
			raw, err = store.GetSecret("ns:n1:db")
			require.NoError(t, err)
			assert.NotEqual(t, "observer", raw.Value, "encrypted together with everything else")
			got, err := svc.GetSecret("ns:n1:db")
			require.NoError(t, err)
			assert.Equal(t, "observer", got.Value)

			require.NoError(t, svc.SaveSecret(Secret{SecretMeta: SecretMeta{ID: "ns:n1:other", Name: "other", Type: SecretNamespace, Scope: "ns:n1"}, Value: "v"}))
			raw, err = store.GetSecret("ns:n1:other")
			require.NoError(t, err)
			assert.NotEqual(t, "v", raw.Value, "and every later write is encrypted")
		})
	}
}
