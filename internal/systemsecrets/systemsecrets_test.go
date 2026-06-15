package systemsecrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Get sources a system secret value through a four-step priority chain that
// migrates older locations into the new plain launcher_state slot:
//  1. launcher_state plain (new home) — return as-is.
//  2. SecretService SYSTEM row — migrate to plain + delete.
//  3. conf/secrets/<id-without-underscore>-secret plain file — migrate + delete.
//  4. Generate fresh — persist under launcher_state.
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

func TestGet_PriorityPlainState(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	// Seed level 1 directly. Get should return it without calling generate
	// or touching anything else.
	require.NoError(t, store.SetStateValue(Key(IDJWT), "preexisting"))

	called := false
	got, err := Get(store, svc, IDJWT, func() string {
		called = true
		return "should-not-fire"
	})
	require.NoError(t, err)
	assert.Equal(t, "preexisting", got)
	assert.False(t, called, "generate must not run when level 1 hits")
}

func TestGet_PriorityPlainFileMigrates(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	// Seed level 3: the plain file fallback. The id "_admin_password" maps
	// to conf/admin_password-secret (strip leading underscore + "-secret"
	// suffix). The value happens to be a literal password for simplicity.
	plainPath := filepath.Join(config.ConfDir(), "admin_password-secret")
	require.NoError(t, os.WriteFile(plainPath, []byte("filevalue"), 0o600))

	called := false
	got, err := Get(store, svc, IDAdminPassword, func() string {
		called = true
		return "fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "filevalue", got, "level 3 hit must return the plain file value")
	assert.False(t, called, "generate must not run when level 3 hits")

	// Migrated INTO launcher_state.
	v, err := store.GetStateValue(Key(IDAdminPassword))
	require.NoError(t, err)
	assert.Equal(t, "filevalue", v, "value must be persisted into launcher_state")

	// Migrated OUT of conf/<file>-secret — deleting the source is what makes
	// the priority chain converge on the new location after a single boot.
	_, statErr := os.Stat(plainPath)
	assert.True(t, os.IsNotExist(statErr),
		"plain file fallback must be removed after migration, got stat err=%v", statErr)
}

func TestGet_PriorityGenerate(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	called := 0
	got, err := Get(store, svc, IDOIDC, func() string {
		called++
		return "generated-fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", got)
	assert.Equal(t, 1, called, "generate must run exactly once when all earlier levels miss")

	// Generated value persisted so the next resolve hits level 1 (i.e. no
	// regenerate on the second boot).
	v, err := store.GetStateValue(Key(IDOIDC))
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", v)

	// Second resolve must NOT regenerate (level 1 hit).
	calledAgain := false
	got2, err := Get(store, svc, IDOIDC, func() string {
		calledAgain = true
		return "would-be-different"
	})
	require.NoError(t, err)
	assert.Equal(t, "generated-fresh", got2, "level 1 must override later levels on second boot")
	assert.False(t, calledAgain)
}

func TestGet_EmptyGenerateIsError(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	_, err := Get(store, svc, "_zzz", func() string { return "" })
	require.Error(t, err, "generate returning empty must surface as an error, not silently persist nothing")
}

// TestGet_PrioritySecretServiceMigrates covers level 2: a SYSTEM row sitting
// in SecretService (a legacy install from before the launcher_state plain
// slot existed) must migrate forward and the source must be deleted. Needs an
// unlocked encrypted SecretService since the service refuses to save anything
// when unencrypted.
func TestGet_PrioritySecretServiceMigrates(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	require.NoError(t, svc.SetMasterPassword("test-password", false))

	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:   IDCiteckSA,
			Type: storage.SecretSystem,
		},
		Value: "legacy-sa-value",
	}))

	called := false
	got, err := Get(store, svc, IDCiteckSA, func() string {
		called = true
		return "fresh"
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy-sa-value", got, "level 2 hit must return the SecretService value")
	assert.False(t, called, "generate must not run when level 2 hits")

	v, err := store.GetStateValue(Key(IDCiteckSA))
	require.NoError(t, err)
	assert.Equal(t, "legacy-sa-value", v, "value must be persisted into launcher_state")

	_, err = svc.GetSecret(IDCiteckSA)
	assert.Error(t, err, "SecretService row must be deleted after migration so level 1 wins next time")
}

// TestMigrateJWTSecretToStdBase64 covers the JWT re-encoding shim: a value
// already in standard base64 passes through untouched, a RawURLEncoding value
// (old launcher versions) is re-encoded to StdEncoding, and a non-base64
// value is kept as-is (logged, never mangled).
func TestMigrateJWTSecretToStdBase64(t *testing.T) {
	raw := []byte{0xfb, 0xef, 0xbe, 0x01, 0x02, 0xff, 0x00, 0x7f, 0xde, 0xad, 0xbe, 0xef}
	std := base64.StdEncoding.EncodeToString(raw)       // contains '+' / '/' + '=' padding
	rawURL := base64.RawURLEncoding.EncodeToString(raw) // '-' / '_', no padding
	require.NotEqual(t, std, rawURL, "fixture must actually differ between encodings")

	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"already standard base64 passes through", std, std},
		{"RawURLEncoding is re-encoded to StdEncoding", rawURL, std},
		{"non-base64 kept as-is", "not base64 at all!!", "not base64 at all!!"},
		{"empty kept as-is", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, migrateJWTSecretToStdBase64(tt.stored))
		})
	}
}

// TestSet_PersistsStateAndDeletesLegacyRow is the package-level statement of
// the rotation-persistence contract: Set writes the priority-1 plain state
// key (what Get reads first on the next daemon start) and removes any legacy
// SecretService row so the priority-2 fallback can never resurrect a stale
// value. The HTTP-level regression lives in
// internal/daemon/routes_admin_password_rotation_test.go.
func TestSet_PersistsStateAndDeletesLegacyRow(t *testing.T) {
	store, svc := newSysSecretFixture(t)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	// Pre-existing install: old value in state + lingering legacy row.
	require.NoError(t, store.SetStateValue(Key(IDAdminPassword), "old-password"))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: IDAdminPassword, Name: IDAdminPassword, Type: storage.SecretSystem},
		Value:      "old-password",
	}))

	require.NoError(t, Set(store, svc, IDAdminPassword, "rotated-pw"))

	v, err := store.GetStateValue(Key(IDAdminPassword))
	require.NoError(t, err)
	assert.Equal(t, "rotated-pw", v)

	_, err = svc.GetSecret(IDAdminPassword)
	require.Error(t, err, "legacy SecretService row must be deleted after Set")

	// And the value Get resolves next boot is the rotated one.
	got, err := Get(store, svc, IDAdminPassword, func() string {
		t.Fatal("generate must not run — the rotated value must already be persisted")
		return ""
	})
	require.NoError(t, err)
	assert.Equal(t, "rotated-pw", got)
}

// TestSet_NilServiceIsFine: CLI callers may not have a SecretService at hand;
// Set must still persist the state key.
func TestSet_NilServiceIsFine(t *testing.T) {
	store, _ := newSysSecretFixture(t)
	require.NoError(t, Set(store, nil, IDJWT, "value-1"))
	v, err := store.GetStateValue(Key(IDJWT))
	require.NoError(t, err)
	assert.Equal(t, "value-1", v)
}

// TestInspect_ReportsAllHomesInPriorityOrder: Inspect is the read-only view
// behind `citeck diagnose --secrets` — it must list every populated home,
// first entry = what Get would return, and must NOT migrate or delete
// anything (that's Get's job).
func TestInspect_ReportsAllHomesInPriorityOrder(t *testing.T) {
	store, svc := newSysSecretFixture(t)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	require.NoError(t, store.SetStateValue(Key(IDJWT), "state-value"))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: IDJWT, Name: IDJWT, Type: storage.SecretSystem},
		Value:      "row-value",
	}))
	plainPath := PlainFilePath(IDJWT)
	require.NoError(t, os.WriteFile(plainPath, []byte("file-value"), 0o600))

	locs := Inspect(store, svc, IDJWT)
	require.Len(t, locs, 3)
	assert.Equal(t, SourceState, locs[0].Source)
	assert.Equal(t, "state-value", locs[0].Value)
	assert.Equal(t, SourceSecretService, locs[1].Source)
	assert.Equal(t, "row-value", locs[1].Value)
	assert.Equal(t, SourcePlainFile, locs[2].Source)
	assert.Equal(t, "file-value", locs[2].Value)

	// Read-only: every home still holds its value afterwards.
	v, err := store.GetStateValue(Key(IDJWT))
	require.NoError(t, err)
	assert.Equal(t, "state-value", v)
	sec, err := svc.GetSecret(IDJWT)
	require.NoError(t, err)
	assert.Equal(t, "row-value", sec.Value)
	_, statErr := os.Stat(plainPath)
	require.NoError(t, statErr, "Inspect must not delete the plain file")

	// Lookup returns the effective (priority-1) location.
	loc, ok := Lookup(store, svc, IDJWT)
	require.True(t, ok)
	assert.Equal(t, SourceState, loc.Source)
	assert.Equal(t, "state-value", loc.Value)
}

func TestInspect_EmptyAndLockedService(t *testing.T) {
	store, svc := newSysSecretFixture(t)

	assert.Empty(t, Inspect(store, svc, IDOIDC), "no homes populated → no locations")
	_, ok := Lookup(store, svc, IDOIDC)
	assert.False(t, ok)

	// A locked SecretService (custom master password, then reopened) simply
	// contributes nothing — Inspect must not error or block on it.
	require.NoError(t, svc.SetMasterPassword("custom-master", false))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: IDOIDC, Name: IDOIDC, Type: storage.SecretSystem},
		Value:      "hidden",
	}))
	locked, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, locked.IsLocked())
	assert.Empty(t, Inspect(store, locked, IDOIDC), "locked service rows are treated as absent")
}
