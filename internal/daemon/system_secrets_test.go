package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// resolveOneSystemSecret sources a system secret value through a four-step
// priority chain that migrates older locations into the new plain
// launcher_state slot:
//   1. launcher_state plain (new home) — return as-is.
//   2. SecretService SYSTEM row — migrate to plain + delete.
//   3. conf/secrets/<id-without-underscore>-secret plain file — migrate + delete.
//   4. Generate fresh — persist under launcher_state.
//
// These tests cover each priority level in isolation so a future change that
// reorders the chain (or breaks the migration delete step) surfaces immediately
// instead of producing a silently-wrong secret on next startup.

func newSysSecretFixture(t *testing.T) (storage.Store, *storage.SecretService) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	require.NoError(t, os.MkdirAll(config.ConfDir(), 0o755))

	store, err := storage.NewSQLiteStore(home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	return store, svc
}

func TestResolveOneSystemSecret_PriorityPlainState(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	// Seed level 1 directly. resolveOneSystemSecret should return it
	// without calling generate or touching anything else.
	require.NoError(t, store.SetStateValue(sysSecretKey("_jwt"), "preexisting"))

	called := false
	got, err := resolveOneSystemSecret(store, svc, "_jwt", func() string {
		called = true
		return "should-not-fire"
	})
	require.NoError(t, err)
	assert.Equal(t, "preexisting", got)
	assert.False(t, called, "generate must not run when level 1 hits")
}

func TestResolveOneSystemSecret_PriorityPlainFileMigrates(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	// Seed level 3: the plain file fallback. The id "_admin_password" maps
	// to conf/admin_password-secret (strip leading underscore + "-secret"
	// suffix). The value happens to be a literal password for simplicity.
	plainPath := filepath.Join(config.ConfDir(), "admin_password-secret")
	require.NoError(t, os.WriteFile(plainPath, []byte("filevalue"), 0o600))

	called := false
	got, err := resolveOneSystemSecret(store, svc, "_admin_password", func() string {
		called = true
		return "fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "filevalue", got, "level 3 hit must return the plain file value")
	assert.False(t, called, "generate must not run when level 3 hits")

	// Migrated INTO launcher_state.
	v, err := store.GetStateValue(sysSecretKey("_admin_password"))
	require.NoError(t, err)
	assert.Equal(t, "filevalue", v, "value must be persisted into launcher_state")

	// Migrated OUT of conf/<file>-secret — deleting the source is what makes
	// the priority chain converge on the new location after a single boot.
	_, statErr := os.Stat(plainPath)
	assert.True(t, os.IsNotExist(statErr),
		"plain file fallback must be removed after migration, got stat err=%v", statErr)
}

func TestResolveOneSystemSecret_PriorityGenerate(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	called := 0
	got, err := resolveOneSystemSecret(store, svc, "_oidc", func() string {
		called++
		return "generated-fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", got)
	assert.Equal(t, 1, called, "generate must run exactly once when all earlier levels miss")

	// Generated value persisted so the next resolve hits level 1 (i.e. no
	// regenerate on the second boot).
	v, err := store.GetStateValue(sysSecretKey("_oidc"))
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", v)

	// Second resolve must NOT regenerate (level 1 hit).
	calledAgain := false
	got2, err := resolveOneSystemSecret(store, svc, "_oidc", func() string {
		calledAgain = true
		return "would-be-different"
	})
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", got2, "level 1 must override later levels on second boot")
	assert.False(t, calledAgain)
}

func TestResolveOneSystemSecret_EmptyGenerateIsError(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	_, err := resolveOneSystemSecret(store, svc, "_zzz", func() string { return "" })
	require.Error(t, err, "generate returning empty must surface as an error, not silently persist nothing")
}

// TestResolveOneSystemSecret_PrioritySecretServiceMigrates covers level 2:
// a SYSTEM row sitting in SecretService (a legacy install from before the
// launcher_state plain slot existed) must migrate forward and the source
// must be deleted. Needs an unlocked encrypted SecretService since the
// service refuses to save anything when unencrypted.
func TestResolveOneSystemSecret_PrioritySecretServiceMigrates(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	require.NoError(t, svc.SetMasterPassword("test-password", false))

	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:   "_citeck_sa",
			Type: storage.SecretSystem,
		},
		Value: "legacy-sa-value",
	}))

	called := false
	got, err := resolveOneSystemSecret(store, svc, "_citeck_sa", func() string {
		called = true
		return "fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy-sa-value", got, "level 2 hit must return the SecretService value")
	assert.False(t, called, "generate must not run when level 2 hits")

	v, err := store.GetStateValue(sysSecretKey("_citeck_sa"))
	require.NoError(t, err)
	assert.Equal(t, "legacy-sa-value", v, "value must be persisted into launcher_state")

	_, err = svc.GetSecret("_citeck_sa")
	assert.Error(t, err, "SecretService row must be deleted after migration so level 1 wins next time")
}
