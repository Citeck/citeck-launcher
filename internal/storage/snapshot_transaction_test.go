package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func transactionFixture(t *testing.T, store Store) (*SecretService, SnapshotState, string) {
	t.Helper()
	svc, err := NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword("transaction-fixture-vault", false))
	id := NamespaceSecretID("ws", "ns", "key")
	require.NoError(t, svc.SaveSecret(Secret{SecretMeta: SecretMeta{ID: id, Name: "key", Type: SecretNamespace, Scope: NamespaceSecretScope("ws", "ns")}, Value: "fixture-old"}))
	state := SnapshotState{WorkspaceID: "ws", NamespaceID: "ns", Status: "STOPPED", JSON: `{"status":"STOPPED","snapshotImportPending":true,"pin":"old"}`}
	require.NoError(t, store.SaveNamespaceState("ws", "ns", state.Status, state.JSON))
	return svc, state, id
}

func TestSQLiteSnapshotTransactionRollsBackSecretsWhenStateFails(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	svc, before, id := transactionFixture(t, store)
	_, err = store.DB().Exec(`CREATE TRIGGER fail_snapshot_state BEFORE UPDATE OF state_json ON namespaces BEGIN SELECT RAISE(ABORT,'fixture state write failure'); END`)
	require.NoError(t, err)
	after := before
	after.JSON = `{"status":"STOPPED","pin":"new"}`
	require.Error(t, svc.CommitNamespaceSnapshot(after, map[string]string{"key": "fixture-new", "added": "fixture-added"}))
	secret, err := svc.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-old", secret.Value)
	_, err = svc.GetSecret(NamespaceSecretID("ws", "ns", "added"))
	require.Error(t, err)
	raw, _, err := store.LoadNamespaceState("ws", "ns")
	require.NoError(t, err)
	require.Equal(t, before.JSON, raw)
	_, err = store.DB().Exec(`DROP TRIGGER fail_snapshot_state`)
	require.NoError(t, err)
	require.NoError(t, svc.CommitNamespaceSnapshot(after, map[string]string{"key": "fixture-new"}))
	secret, err = svc.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-new", secret.Value)
	stored, err := store.GetSecret(id)
	require.NoError(t, err)
	require.NotEqual(t, secret.Value, stored.Value, "transaction stores destination ciphertext")
	raw, _, err = store.LoadNamespaceState("ws", "ns")
	require.NoError(t, err)
	require.Equal(t, after.JSON, raw)
}

func TestFileSnapshotFailureBlocksReadsUntilRecovery(t *testing.T) {
	base := t.TempDir()
	store, err := NewFileStore(filepath.Join(base, "conf"), filepath.Join(base, "runtime"))
	require.NoError(t, err)
	svc, before, id := transactionFixture(t, store)
	after := before
	after.JSON = `{"status":"STOPPED","pin":"new"}`
	// The first credential write succeeds; the state directory then refuses
	// writes. Rollback cannot finish while that filesystem is unavailable.
	dir := filepath.Dir(store.nsStatePath("ns"))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err = svc.CommitNamespaceSnapshot(after, map[string]string{"key": "fixture-new"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "fixture-new")
	_, err = store.GetSecret(id)
	require.Error(t, err, "partial metadata must not be exposed")
	journal, err := os.Stat(store.snapshotJournalPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), journal.Mode().Perm())
	require.NoError(t, os.Chmod(dir, 0o700))
	reopened, err := NewFileStore(store.baseDir, store.runtimeBase)
	require.NoError(t, err)
	require.Same(t, store.mu, reopened.mu)
	secret, err := svc.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-old", secret.Value)
	raw, _, err := reopened.LoadNamespaceState("ws", "ns")
	require.NoError(t, err)
	require.Equal(t, before.JSON, raw)
	require.NoFileExists(t, store.snapshotJournalPath())
	require.NoError(t, svc.CommitNamespaceSnapshot(after, map[string]string{"key": "fixture-new"}))
	secret, err = svc.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-new", secret.Value)
	raw, _, err = store.LoadNamespaceState("ws", "ns")
	require.NoError(t, err)
	require.Equal(t, after.JSON, raw)
}

func TestFileSnapshotCrashRecoveryUsesCommitMarker(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, existed := range []bool{false, true} {
			t.Run(map[bool]string{false: "prepared", true: "committed"}[committed]+map[bool]string{false: "-new-key", true: "-old-key"}[existed], func(t *testing.T) {
				base := t.TempDir()
				store, err := NewFileStore(filepath.Join(base, "conf"), filepath.Join(base, "runtime"))
				require.NoError(t, err)
				state := SnapshotState{WorkspaceID: "ws", NamespaceID: "ns", Status: "STOPPED", JSON: `{"status":"STOPPED","pin":"new"}`}
				oldState := []byte(`{"status":"STOPPED","snapshotImportPending":true,"pin":"old"}`)
				require.NoError(t, store.SaveNamespaceState("ws", "ns", "STOPPED", string(oldState)))
				id := NamespaceSecretID("ws", "ns", "key")
				row := Secret{SecretMeta: SecretMeta{ID: id, Name: "key", Type: SecretNamespace, Scope: NamespaceSecretScope("ws", "ns")}, Value: "fixture-old"}
				var before []byte
				if existed {
					require.NoError(t, store.SaveSecret(row))
					before, err = os.ReadFile(store.secretPath(id))
					require.NoError(t, err)
				}
				row.Value = "fixture-new"
				after, err := json.Marshal(storedSecretFile(row))
				require.NoError(t, err)
				j := snapshotFileJournal{State: state, Committed: committed, Changes: []snapshotFileChange{{SecretID: id, Before: before, Existed: existed, After: after}, {Before: oldState, Existed: true, After: []byte(state.JSON)}}}
				require.NoError(t, store.writeSnapshotJournal(j))
				// Simulate interruption after only the credential file changed.
				partial := j
				partial.Changes = j.Changes[:1]
				require.NoError(t, store.applySnapshotJournal(partial, true))
				reopened, err := NewFileStore(store.baseDir, store.runtimeBase)
				require.NoError(t, err)
				secret, secretErr := reopened.GetSecret(id)
				raw, _, err := reopened.LoadNamespaceState("ws", "ns")
				require.NoError(t, err)
				if committed {
					require.NoError(t, secretErr)
					require.Equal(t, "fixture-new", secret.Value)
					require.Equal(t, state.JSON, raw)
				} else {
					require.Equal(t, string(oldState), raw)
					if existed {
						require.NoError(t, secretErr)
						require.Equal(t, "fixture-old", secret.Value)
					} else {
						require.Error(t, secretErr)
					}
				}
				require.NoFileExists(t, store.snapshotJournalPath())
			})
		}
	}
}

func TestSnapshotTransactionPreservesExistingNamespaceRowIdentity(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	svc, state, _ := transactionFixture(t, store)
	meta := SecretMeta{ID: "legacy-namespace-row", Name: "legacy-key", Type: SecretNamespace, Scope: NamespaceSecretScope("ws", "ns")}
	require.NoError(t, svc.SaveSecret(Secret{SecretMeta: meta, Value: "fixture-old"}))
	require.NoError(t, svc.CommitNamespaceSnapshot(state, map[string]string{"legacy-key": "fixture-new"}))
	row, err := svc.GetSecret(meta.ID)
	require.NoError(t, err)
	require.Equal(t, "fixture-new", row.Value)
	_, err = store.GetSecret(NamespaceSecretID("ws", "ns", "legacy-key"))
	require.Error(t, err, "restore must not create a second row with the same name")
}
