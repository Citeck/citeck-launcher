package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

func newAdminPwTestStore(t *testing.T) *storage.FileStore {
	t.Helper()
	dir := t.TempDir()
	// The lookup chain also probes the pre-Store plain file under
	// config.ConfDir() — pin CITECK_HOME so the test never reads a real
	// install's conf/admin_password-secret.
	t.Setenv("CITECK_HOME", dir)
	require.NoError(t, os.MkdirAll(config.ConfDir(), 0o755))
	store, err := storage.NewFileStore(dir, filepath.Join(dir, "runtime"))
	require.NoError(t, err)
	return store
}

// The daemon stores the generated admin password as the plain launcher_state
// key "_sys_admin_password" (systemsecrets.Key(systemsecrets.IDAdminPassword)).
// The install wizard must read that key first.
func TestReadAdminPasswordFromOpenStore_PlainStateKey(t *testing.T) {
	store := newAdminPwTestStore(t)
	require.NoError(t, store.SetStateValue("_sys_admin_password", "random-pw-123"))

	require.Equal(t, "random-pw-123", readAdminPasswordFromOpenStore(store))
}

// Older installs kept the admin password as an encrypted SecretService row
// "_admin_password" (default master password). That legacy location must
// still be readable as a fallback.
func TestReadAdminPasswordFromOpenStore_LegacySecretServiceFallback(t *testing.T) {
	store := newAdminPwTestStore(t)

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "_admin_password", Name: "_admin_password", Type: storage.SecretSystem},
		Value:      "legacy-pw-456",
	}))

	require.Equal(t, "legacy-pw-456", readAdminPasswordFromOpenStore(store))
}

// The plain state key wins over the legacy SecretService row when both exist
// (the daemon migrates the row on start, but the wizard may run mid-migration).
func TestReadAdminPasswordFromOpenStore_PlainStateWinsOverLegacy(t *testing.T) {
	store := newAdminPwTestStore(t)

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "_admin_password", Name: "_admin_password", Type: storage.SecretSystem},
		Value:      "legacy-pw",
	}))
	require.NoError(t, store.SetStateValue("_sys_admin_password", "current-pw"))

	require.Equal(t, "current-pw", readAdminPasswordFromOpenStore(store))
}

// Fresh store (daemon never started): best-effort empty result, no error.
func TestReadAdminPasswordFromOpenStore_EmptyStore(t *testing.T) {
	store := newAdminPwTestStore(t)
	require.Empty(t, readAdminPasswordFromOpenStore(store))
}

// Pre-Store installs kept the password as a plain conf/admin_password-secret
// file (priority 3 of the systemsecrets chain) — the wizard must read it too
// when neither newer home is populated.
func TestReadAdminPasswordFromOpenStore_PlainFileFallback(t *testing.T) {
	store := newAdminPwTestStore(t)
	plainPath := systemsecrets.PlainFilePath(systemsecrets.IDAdminPassword)
	require.NoError(t, os.WriteFile(plainPath, []byte("file-pw-789"), 0o600))

	require.Equal(t, "file-pw-789", readAdminPasswordFromOpenStore(store))

	// Read-only: the wizard must not migrate or delete (that's the daemon's
	// systemsecrets.Get job).
	_, statErr := os.Stat(plainPath)
	require.NoError(t, statErr)
}

// Encrypted with a NON-default master password: the wizard cannot unlock,
// must return "" instead of failing.
func TestReadAdminPasswordFromOpenStore_CustomMasterPasswordLocked(t *testing.T) {
	store := newAdminPwTestStore(t)

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("user-master-pw", false))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "_admin_password", Name: "_admin_password", Type: storage.SecretSystem},
		Value:      "unreachable-pw",
	}))

	require.Empty(t, readAdminPasswordFromOpenStore(store))
}
