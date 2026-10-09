//go:build integration

package snapshot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
	"github.com/stretchr/testify/require"
)

// Uses only scratch bind directories and temporary utils containers, no daemon
// and no mutation of any existing namespace or its Docker volumes.
func TestSnapshotSecretsDockerRoundTrip(t *testing.T) {
	config.SetDesktopMode(false)
	dc, err := docker.NewClient("snapshot-secrets-fixture", "snapshot-secrets-fixture")
	require.NoError(t, err)
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	source, _ := snapshotVault(t, "source-fixture-vault")
	putNamespaceSecret(t, source, "original-ws", "original-ns", "seaweedfs-s3-key", "fixture-original")
	dir := t.TempDir()
	path := filepath.Join(dir, "volumes", "postgres3")
	require.NoError(t, os.MkdirAll(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "payload"), []byte("fixture-data-that-requires-the-original-key"), 0o600))
	archive := filepath.Join(t.TempDir(), "snapshot.zip")
	_, err = Export(ctx, dc, archive, dir, nil, WithSystemSecrets(NamespaceSecrets(source, "original-ws", "original-ns"), map[string]string{systemsecrets.NamespaceName(systemsecrets.IDAdminPassword): "fixture-admin", systemsecrets.NamespaceName(systemsecrets.IDCiteckSA): "fixture-sa", systemsecrets.NamespaceName(systemsecrets.IDOIDC): "fixture-oidc"}), map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2}}, nil)
	require.NoError(t, err)
	target, _ := snapshotVault(t, "destination-fixture-vault")
	putNamespaceSecret(t, target, "new-ws", "new-ns", "seaweedfs-s3-key", "fixture-target-before-import")
	out := t.TempDir()
	meta, err := Import(ctx, dc, archive, out, NamespaceSecrets(target, "new-ws", "new-ns"), nil)
	require.NoError(t, err)
	require.Equal(t, "postgres:17.5", meta.Dependencies[deps.Postgres].Image)
	require.Equal(t, 2, meta.Dependencies[deps.Postgres].Gen())
	raw, err := os.ReadFile(filepath.Join(out, "volumes", "postgres3", "payload"))
	require.NoError(t, err)
	require.Equal(t, "fixture-data-that-requires-the-original-key", string(raw))
	values, err := NamespaceSecrets(target, "new-ws", "new-ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-original", values["seaweedfs-s3-key"])

	for id, expected := range map[string]string{systemsecrets.IDAdminPassword: "fixture-admin", systemsecrets.IDCiteckSA: "fixture-sa", systemsecrets.IDOIDC: "fixture-oidc"} {
		value, exists, lookupErr := systemsecrets.NamespaceOverride(target, "new-ws", "new-ns", id)
		require.NoError(t, lookupErr)
		require.True(t, exists)
		require.Equal(t, expected, value)
	}

	// Default export/import on the same server preserves all existing credentials.
	plainArchive := filepath.Join(t.TempDir(), "without-secrets.zip")
	_, err = Export(ctx, dc, plainArchive, dir, nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = Import(ctx, dc, plainArchive, out, NamespaceSecrets(target, "new-ws", "new-ns"), nil)
	require.NoError(t, err)
	after, err := NamespaceSecrets(target, "new-ws", "new-ns").Read()
	require.NoError(t, err)
	require.Equal(t, values, after)

	// A damaged inner tar must fail before replacing restored data or credentials.
	brokenDir := t.TempDir()
	brokenMeta, err := json.Marshal(NamespaceSnapshotMeta{Volumes: []VolumeSnapshotMeta{{Name: "postgres3", DataFile: "postgres3.tar.zst"}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(brokenDir, metaFileName), brokenMeta, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(brokenDir, "postgres3.tar.zst"), []byte("not-a-zstd-archive"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(brokenDir, secretsFileName), []byte(`{"seaweedfs-s3-key":"fixture-must-not-land"}`), 0o600))
	brokenZip := filepath.Join(t.TempDir(), "broken.zip")
	require.NoError(t, createZip(brokenZip, brokenDir))
	_, err = Import(ctx, dc, brokenZip, out, NamespaceSecrets(target, "new-ws", "new-ns"), nil)
	require.Error(t, err)
	require.FileExists(t, filepath.Join(out, "volumes", "postgres3", "payload"))
	values, err = NamespaceSecrets(target, "new-ws", "new-ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-original", values["seaweedfs-s3-key"])
}
