package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
	"github.com/stretchr/testify/require"
)

func TestRestoreSnapshotPinsBeforeFirstLoadPreservesOtherState(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	before := namespace.NsPersistedState{Status: namespace.NsStatusStopped, ManualStoppedApps: []string{"mailpit"}, Dependencies: map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 3}, deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
	}, DependencyMigration: &deps.MigrationJournal{ID: deps.Postgres}}
	raw, err := json.Marshal(before)
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceState("ws", "target", string(before.Status), string(raw)))
	meta := &snapshot.NamespaceSnapshotMeta{Volumes: []snapshot.VolumeSnapshotMeta{{Name: "postgres3"}}, Dependencies: map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2}, deps.Keycloak: {Image: "keycloak/keycloak:26.4.5"},
	}}
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	plan, err := SnapshotDestination(store, svc, "ws", "target", nil).(snapshot.StateStore).PrepareSnapshotRestore(meta, nil)
	require.NoError(t, err)
	require.NoError(t, plan.Begin())
	require.NoError(t, plan.Commit())
	state := loadNsStateFromStore(store, "ws", "target")
	require.Equal(t, meta.Dependencies[deps.Postgres], state.Dependencies[deps.Postgres])
	require.Equal(t, before.Dependencies[deps.RabbitMQ], state.Dependencies[deps.RabbitMQ])
	require.Equal(t, before.ManualStoppedApps, state.ManualStoppedApps)
	require.Nil(t, state.DependencyMigration)
	_, other, err := store.LoadNamespaceState("ws", "other")
	require.NoError(t, err)
	require.False(t, other)
}

func TestImportedGeneratedSecretsSurviveFirstNamespaceGeneration(t *testing.T) {
	svc, _ := newNsSecretService(t)
	original, err := loadNamespaceSecrets(svc, "source-ws", "source", &bundle.WorkspaceConfig{Secrets: []bundle.SecretDefault{{ID: "seaweedfs-s3-key", Generated: true, Length: 40}}}, nsSecretsSeed)
	require.NoError(t, err)
	values, err := snapshot.NamespaceSecrets(svc, "source-ws", "source").Read()
	require.NoError(t, err)
	restore, err := snapshot.NamespaceSecrets(svc, "target-ws", "target").PrepareRestore(values)
	require.NoError(t, err)
	require.NoError(t, restore())
	actual, err := loadNamespaceSecrets(svc, "target-ws", "target", &bundle.WorkspaceConfig{Secrets: []bundle.SecretDefault{{ID: "seaweedfs-s3-key", Generated: true, Length: 128}}}, nsSecretsSeed)
	require.NoError(t, err)
	require.Equal(t, original["seaweedfs-s3-key"], actual["seaweedfs-s3-key"])
}

func TestSnapshotSystemCredentialsRemainNamespaceScoped(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("snapshot-target-fixture", false))
	source := namespace.SystemSecrets{JWT: "fixture-jwt", OIDC: "fixture-oidc", AdminPassword: "fixture-admin", CiteckSA: "fixture-sa"}
	base := snapshot.NamespaceSecrets(svc, "ws", "source")
	values, err := snapshot.WithSystemSecrets(base, snapshotSystemValues(source)).Read()
	require.NoError(t, err)
	restore, err := snapshot.NamespaceSecrets(svc, "other-ws", "target").PrepareRestore(values)
	require.NoError(t, err)
	require.NoError(t, restore())
	defaults := namespace.SystemSecrets{JWT: "target-jwt", OIDC: "target-oidc", AdminPassword: "target-admin", CiteckSA: "target-sa"}
	got, err := applyNamespaceSystemSecrets(svc, "other-ws", "target", defaults)
	require.NoError(t, err)
	require.Equal(t, source, got)

	d := &Daemon{secretService: svc, activeNs: &activeNamespace{workspaceID: "other-ws", nsConfig: &namespace.Config{ID: "target"}, systemSecrets: defaults}}
	require.NoError(t, d.refreshSnapshotSystemSecrets(d.active()))
	require.Equal(t, source, d.active().systemSecrets)
	foreign := activeNamespace{workspaceID: "ws", nsConfig: &namespace.Config{ID: "target"}, systemSecrets: defaults}
	require.NoError(t, d.refreshSnapshotSystemSecrets(foreign))
	require.Equal(t, source, d.active().systemSecrets)
	other, err := applyNamespaceSystemSecrets(svc, "ws", "target", defaults)
	require.NoError(t, err)
	require.Equal(t, defaults, other)
	require.NoError(t, systemsecrets.Set(store, svc, systemsecrets.IDAdminPassword, "global-fixture"))
	require.NoError(t, systemsecrets.SetForNamespace(store, svc, "other-ws", "target", systemsecrets.IDAdminPassword, "rotated-fixture"))
	got, err = applyNamespaceSystemSecrets(svc, "other-ws", "target", defaults)
	require.NoError(t, err)
	require.Equal(t, "rotated-fixture", got.AdminPassword)
	loc, ok := systemsecrets.Lookup(store, svc, systemsecrets.IDAdminPassword)
	require.True(t, ok)
	require.Equal(t, "global-fixture", loc.Value)
	svc, err = storage.NewSecretService(store)
	require.NoError(t, err)
	_, err = applyNamespaceSystemSecrets(svc, "other-ws", "target", defaults)
	require.ErrorIs(t, err, storage.ErrSecretsLocked)
	other, err = applyNamespaceSystemSecrets(svc, "ws", "target", defaults)
	require.NoError(t, err)
	require.Equal(t, defaults, other)
}

func TestSnapshotTransactionFailureKeepsRuntimePinsAndStartGate(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("transaction-fixture-vault", false))
	rt := namespace.NewRuntime(&namespace.Config{ID: "target"}, planStubDocker{}, t.TempDir())
	defer rt.Shutdown()
	rt.SetStatePersister(nsStatePersister{store: store, wsID: "ws", nsID: "target"})
	old := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18", VolumeGen: 2}}
	rt.RestoreDependencyState(old, nil, nil)
	meta := &snapshot.NamespaceSnapshotMeta{Volumes: []snapshot.VolumeSnapshotMeta{{Name: "postgres3"}}, Dependencies: map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2}}}
	plan, err := SnapshotDestination(store, svc, "ws", "target", rt).(snapshot.StateStore).PrepareSnapshotRestore(meta, map[string]string{"key": "fixture-key"})
	require.NoError(t, err)
	require.NoError(t, plan.Begin())
	require.True(t, rt.SnapshotImportPending())
	_, err = store.DB().Exec(`CREATE TRIGGER fail_snapshot_state BEFORE UPDATE OF state_json ON namespaces BEGIN SELECT RAISE(ABORT,'fixture state write failure'); END`)
	require.NoError(t, err)
	require.Error(t, plan.Commit())
	require.Equal(t, old, rt.DependencyStates())
	require.True(t, rt.SnapshotImportPending())
	require.True(t, loadNsStateFromStore(store, "ws", "target").SnapshotImportPending)
	require.Error(t, rt.StartApp("postgres"))
	_, err = svc.GetSecret(storage.NamespaceSecretID("ws", "target", "key"))
	require.Error(t, err)
	_, err = store.DB().Exec(`DROP TRIGGER fail_snapshot_state`)
	require.NoError(t, err)
	require.NoError(t, plan.Commit())
	require.False(t, rt.SnapshotImportPending())
	require.Equal(t, meta.Dependencies, rt.DependencyStates())
	require.False(t, loadNsStateFromStore(store, "ws", "target").SnapshotImportPending)
}

func TestIncompleteSnapshotBlocksStartAndExport(t *testing.T) {
	for _, path := range []string{api.NamespaceStart, api.SnapshotsExport, api.AppStart("postgres"), api.AppRestart("postgres")} {
		t.Run(path, func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			d.active().runtime.RestoreSnapshotImportPending(true)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, http.NoBody))
			require.Equal(t, http.StatusConflict, response.Code)
			require.Contains(t, response.Body.String(), "Snapshot import is incomplete")
		})
	}
}
