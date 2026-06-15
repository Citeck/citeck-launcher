package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

// buildSecretsChecks is the engine behind `citeck diagnose --secrets`: a
// strictly read-only consistency audit of the three homes a system secret
// can live in. These tests pin the status mapping (missing→warning,
// healthy→ok, identical shadow→warning, diverging shadow→error+hint,
// locked SecretService→warning) and the read-only contract.

func newSecretsAuditFixture(t *testing.T) (storage.Store, *storage.SecretService) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CITECK_HOME", dir) // pin PlainFilePath probes to the temp dir
	require.NoError(t, os.MkdirAll(config.ConfDir(), 0o755))
	store, err := storage.NewFileStore(dir, filepath.Join(dir, "runtime"))
	require.NoError(t, err)
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	return store, svc
}

// checksByName indexes audit checks per name; a name can repeat (one entry
// per shadowed home), hence the slice value.
func checksByName(checks []diagnoseCheck) map[string][]diagnoseCheck {
	m := make(map[string][]diagnoseCheck)
	for _, c := range checks {
		m[c.Name] = append(m[c.Name], c)
	}
	return m
}

func TestBuildSecretsChecks_FreshStoreAllMissing(t *testing.T) {
	store, svc := newSecretsAuditFixture(t)

	checks := buildSecretsChecks(store, svc)
	require.Len(t, checks, len(systemsecrets.KnownIDs()),
		"one check per known id, no locked-storage entry on an unencrypted store")
	for _, c := range checks {
		assert.Equal(t, "warning", c.Status, "missing secret %s must be a warning, not an error", c.Name)
	}
}

func TestBuildSecretsChecks_HealthyStateOnly(t *testing.T) {
	store, svc := newSecretsAuditFixture(t)
	for _, id := range systemsecrets.KnownIDs() {
		require.NoError(t, store.SetStateValue(systemsecrets.Key(id), "value-"+id))
	}

	checks := buildSecretsChecks(store, svc)
	require.Len(t, checks, len(systemsecrets.KnownIDs()))
	for _, c := range checks {
		assert.Equal(t, "ok", c.Status)
		assert.NotContains(t, c.Message, "value-", "audit must never print a raw secret value")
	}
}

func TestBuildSecretsChecks_ShadowedCopies(t *testing.T) {
	store, svc := newSecretsAuditFixture(t)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	// _jwt: identical copy in the legacy SecretService row → harmless WARN
	// (the daemon migrates + deletes it on next start).
	require.NoError(t, store.SetStateValue(systemsecrets.Key(systemsecrets.IDJWT), "same-value"))
	require.NoError(t, svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: systemsecrets.IDJWT, Name: systemsecrets.IDJWT, Type: storage.SecretSystem},
		Value:      "same-value",
	}))

	// _admin_password: DIVERGING copy in the pre-Store plain file → ERROR
	// with a cleanup recommendation (the shadowed-stale-value class of bug).
	require.NoError(t, store.SetStateValue(systemsecrets.Key(systemsecrets.IDAdminPassword), "current-pw"))
	plainPath := systemsecrets.PlainFilePath(systemsecrets.IDAdminPassword)
	require.NoError(t, os.WriteFile(plainPath, []byte("ancient-pw"), 0o600))

	byName := checksByName(buildSecretsChecks(store, svc))

	jwt := byName["secret:"+systemsecrets.IDJWT]
	require.Len(t, jwt, 2, "effective entry + one shadow entry")
	assert.Equal(t, "ok", jwt[0].Status)
	assert.Equal(t, "warning", jwt[1].Status, "identical shadow is a warning, not an error")

	admin := byName["secret:"+systemsecrets.IDAdminPassword]
	require.Len(t, admin, 2)
	assert.Equal(t, "ok", admin[0].Status)
	assert.Equal(t, "error", admin[1].Status, "diverging shadow must be an error")
	assert.NotEmpty(t, admin[1].FixHint, "diverging shadow must carry the cleanup recommendation")
	assert.NotContains(t, admin[1].Message, "ancient-pw", "stale value must be masked")

	// Read-only contract: the audit must not have migrated or deleted anything.
	_, statErr := os.Stat(plainPath)
	require.NoError(t, statErr, "audit must not delete the plain file")
	sec, err := svc.GetSecret(systemsecrets.IDJWT)
	require.NoError(t, err)
	assert.Equal(t, "same-value", sec.Value, "audit must not delete the SecretService row")
}

func TestBuildSecretsChecks_LockedSecretService(t *testing.T) {
	store, svc := newSecretsAuditFixture(t)
	require.NoError(t, svc.SetMasterPassword("custom-master-pw", false))

	locked, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, locked.IsLocked())

	checks := buildSecretsChecks(store, locked)
	require.Len(t, checks, len(systemsecrets.KnownIDs())+1,
		"locked storage adds one extra warning entry")
	assert.Equal(t, "secrets:storage", checks[0].Name)
	assert.Equal(t, "warning", checks[0].Status)
}
