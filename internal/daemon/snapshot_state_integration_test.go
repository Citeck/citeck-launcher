//go:build integration

package daemon

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

type failSnapshotCommitStore struct {
	*storage.FileStore
	fail bool
}

func (s *failSnapshotCommitStore) CommitSnapshotState(state storage.SnapshotState, rows []storage.Secret) error {
	if s.fail {
		return fmt.Errorf("fixture metadata commit failed")
	}
	return s.FileStore.CommitSnapshotState(state, rows)
}

// Pure Docker utils fixtures; no launcher daemon or existing namespace volumes.
func TestSnapshotTransactionDockerFailureAndRetry(t *testing.T) {
	config.SetDesktopMode(false)
	dc, err := docker.NewClient("snapshot-transaction-fixture", "snapshot-transaction-fixture")
	require.NoError(t, err)
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	source, _ := newNsSecretService(t)
	require.NoError(t, source.SaveSecret(storage.Secret{SecretMeta: storage.SecretMeta{ID: storage.NamespaceSecretID("source", "original", "key"), Name: "key", Type: storage.SecretNamespace, Scope: storage.NamespaceSecretScope("source", "original")}, Value: "fixture-source-key"}))
	data := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(data, "volumes", "postgres3"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(data, "volumes", "postgres3", "payload"), []byte("fixture-source-data"), 0o600))
	archive := filepath.Join(t.TempDir(), "snapshot.zip")
	pins := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2}}
	_, err = snapshot.Export(ctx, dc, archive, data, nil, snapshot.NamespaceSecrets(source, "source", "original"), pins, nil)
	require.NoError(t, err)
	base := t.TempDir()
	fileStore, err := storage.NewFileStore(filepath.Join(base, "conf"), filepath.Join(base, "runtime"))
	require.NoError(t, err)
	store := &failSnapshotCommitStore{FileStore: fileStore, fail: true}
	target, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, target.SetMasterPassword("different-target-vault-fixture", false))
	id := storage.NamespaceSecretID("target", "new", "key")
	require.NoError(t, target.SaveSecret(storage.Secret{SecretMeta: storage.SecretMeta{ID: id, Name: "key", Type: storage.SecretNamespace, Scope: storage.NamespaceSecretScope("target", "new")}, Value: "fixture-old-key"}))
	old := namespace.NsPersistedState{Status: namespace.NsStatusStopped, Dependencies: map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18", VolumeGen: 2}}}
	raw, err := json.Marshal(old)
	require.NoError(t, err)
	require.NoError(t, store.SaveNamespaceState("target", "new", string(old.Status), string(raw)))
	out := t.TempDir()
	destination := SnapshotDestination(store, target, "target", "new", nil)
	_, err = snapshot.Import(ctx, dc, archive, out, destination, nil)
	require.Error(t, err)
	payload, err := os.ReadFile(filepath.Join(out, "volumes", "postgres3", "payload"))
	require.NoError(t, err, "data extraction completed before the metadata failure")
	require.Equal(t, "fixture-source-data", string(payload))
	state := loadNsStateFromStore(store, "target", "new")
	require.True(t, state.SnapshotImportPending)
	require.Equal(t, old.Dependencies, state.Dependencies)
	secret, err := target.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-old-key", secret.Value)
	store.fail = false
	_, err = snapshot.Import(ctx, dc, archive, out, destination, nil)
	require.NoError(t, err)
	state = loadNsStateFromStore(store, "target", "new")
	require.False(t, state.SnapshotImportPending)
	require.Equal(t, pins, state.Dependencies)
	secret, err = target.GetSecret(id)
	require.NoError(t, err)
	require.Equal(t, "fixture-source-key", secret.Value)
	stored, err := store.GetSecret(id)
	require.NoError(t, err)
	require.NotEqual(t, secret.Value, stored.Value)

	// Corrupt the inner tar: this must fail before the pending flag or data changes.
	broken := filepath.Join(t.TempDir(), "broken.zip")
	reader, err := zip.OpenReader(archive)
	require.NoError(t, err)
	defer reader.Close()
	output, err := os.Create(broken)
	require.NoError(t, err)
	writer := zip.NewWriter(output)
	for _, entry := range reader.File {
		part, err := writer.Create(entry.Name)
		require.NoError(t, err)
		if strings.HasSuffix(entry.Name, ".tar.zst") {
			_, err = part.Write([]byte("fixture-invalid-zstd"))
			require.NoError(t, err)
			continue
		}
		input, err := entry.Open()
		require.NoError(t, err)
		_, err = io.Copy(part, input)
		require.NoError(t, err)
		require.NoError(t, input.Close())
	}
	require.NoError(t, writer.Close())
	require.NoError(t, output.Close())
	_, err = snapshot.Import(ctx, dc, broken, out, destination, nil)
	require.Error(t, err)
	require.False(t, loadNsStateFromStore(store, "target", "new").SnapshotImportPending)
	payload, err = os.ReadFile(filepath.Join(out, "volumes", "postgres3", "payload"))
	require.NoError(t, err)
	require.Equal(t, "fixture-source-data", string(payload))
}
