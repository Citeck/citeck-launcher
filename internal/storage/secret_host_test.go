package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestSQLiteMigrationV8_BackfillsRegistryHost verifies the v8 upgrade backfills
// the host tag for legacy REGISTRY_AUTH secrets from their "images-repo:<host>"
// scope, so they surface in the host-filtered picker. Non-registry secrets and
// non-conforming scopes are left with an empty host.
func TestSQLiteMigrationV8_BackfillsRegistryHost(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "launcher.db")

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	mustExec := func(q string, args ...any) {
		t.Helper()
		_, execErr := db.Exec(q, args...)
		require.NoError(t, execErr, "exec %q", q)
	}
	// Seed a v7 schema by hand so migrate() picks up at v8.
	mustExec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	mustExec(`INSERT INTO schema_version VALUES (7)`)
	mustExec(`CREATE TABLE secrets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'global',
		username TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	)`)
	mustExec(`INSERT INTO secrets (id, type, scope) VALUES
		('images-repo:harbor.citeck.ru', 'REGISTRY_AUTH', 'images-repo:harbor.citeck.ru'),
		('git-token', 'GIT_TOKEN', 'global'),
		('odd-registry', 'REGISTRY_AUTH', 'global')`)
	require.NoError(t, db.Close())

	store, err := NewSQLiteStore(dir)
	require.NoError(t, err)
	defer store.Close()

	metas, err := store.ListSecrets()
	require.NoError(t, err)
	hosts := map[string]string{}
	for _, m := range metas {
		hosts[m.ID] = m.Host
	}
	assert.Equal(t, "harbor.citeck.ru", hosts["images-repo:harbor.citeck.ru"], "registry secret host backfilled from scope")
	assert.Empty(t, hosts["git-token"], "git token keeps empty host")
	assert.Empty(t, hosts["odd-registry"], "registry secret without images-repo scope keeps empty host")
}

// TestFileStoreRegistryHostBackfill verifies the server-mode equivalent: a
// legacy registry secret JSON without a host field reads back with the host
// derived from its "images-repo:<host>" scope.
func TestFileStoreRegistryHostBackfill(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, filepath.Join(dir, "runtime"))
	require.NoError(t, err)
	defer store.Close()

	// Write a legacy registry secret with no host, scope = images-repo:<host>.
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{
			ID:       "images-repo:enterprise-registry.citeck.ru",
			Type:     SecretRegistryAuth,
			Scope:    "images-repo:enterprise-registry.citeck.ru",
			Username: "svc",
		},
		Value: "pass",
	}))

	got, err := store.GetSecret("images-repo:enterprise-registry.citeck.ru")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "enterprise-registry.citeck.ru", got.Host)

	// An explicitly-saved host wins and round-trips.
	require.NoError(t, store.SaveSecret(Secret{
		SecretMeta: SecretMeta{ID: "reg2", Type: SecretRegistryAuth, Host: "harbor.citeck.ru", Username: "u"},
		Value:      "p",
	}))
	got2, err := store.GetSecret("reg2")
	require.NoError(t, err)
	assert.Equal(t, "harbor.citeck.ru", got2.Host)
}
