package snapshot

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
	"github.com/stretchr/testify/require"
)

func snapshotVault(t *testing.T, password string) (*storage.SecretService, *storage.FileStore) {
	t.Helper()
	base := t.TempDir()
	store, err := storage.NewFileStore(filepath.Join(base, "conf"), filepath.Join(base, "runtime"))
	require.NoError(t, err)
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(password, false))
	return svc, store
}

func putNamespaceSecret(t *testing.T, svc *storage.SecretService, ws, ns, name, value string) {
	t.Helper()
	require.NoError(t, svc.SaveSecret(storage.Secret{SecretMeta: storage.SecretMeta{
		ID: storage.NamespaceSecretID(ws, ns, name), Name: name, Type: storage.SecretNamespace, Scope: storage.NamespaceSecretScope(ws, ns),
	}, Value: value}))
}

func TestNamespaceSnapshotSecretsPortableAcrossVaults(t *testing.T) {
	source, _ := snapshotVault(t, "source-vault-fixture")
	putNamespaceSecret(t, source, "source-ws", "source-ns", "seaweedfs-s3-key", "fixture-source-key")
	putNamespaceSecret(t, source, "source-ws", "source-ns", "empty-constant", "")
	putNamespaceSecret(t, source, "other-ws", "source-ns", "other", "fixture-other")
	require.NoError(t, source.SaveSecret(storage.Secret{SecretMeta: storage.SecretMeta{ID: "global-registry", Name: "registry", Type: storage.SecretRegistryAuth, Scope: "global"}, Value: "fixture-registry"}))
	values, err := NamespaceSecrets(source, "source-ws", "source-ns").Read()
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.Equal(t, "fixture-source-key", values["seaweedfs-s3-key"])
	target, rawStore := snapshotVault(t, "target-vault-fixture")
	putNamespaceSecret(t, target, "target-ws", "target-ns", "seaweedfs-s3-key", "fixture-old")
	putNamespaceSecret(t, target, "target-ws", "target-ns", "target-only", "fixture-retained")
	putNamespaceSecret(t, target, "other-ws", "target-ns", "seaweedfs-s3-key", "fixture-unrelated")
	restore, err := NamespaceSecrets(target, "target-ws", "target-ns").PrepareRestore(values)
	require.NoError(t, err)
	before, err := NamespaceSecrets(target, "target-ws", "target-ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-old", before["seaweedfs-s3-key"], "prepare must not mutate")
	require.NoError(t, restore())
	got, err := NamespaceSecrets(target, "target-ws", "target-ns").Read()
	require.NoError(t, err)
	require.Equal(t, values["seaweedfs-s3-key"], got["seaweedfs-s3-key"])
	require.Equal(t, "fixture-retained", got["target-only"])
	value, exists := got["empty-constant"]
	require.True(t, exists && value == "")
	raw, err := rawStore.GetSecret(storage.NamespaceSecretID("target-ws", "target-ns", "seaweedfs-s3-key"))
	require.NoError(t, err)
	require.NotEqual(t, values["seaweedfs-s3-key"], raw.Value, "target must encrypt with its own vault")
	other, err := NamespaceSecrets(target, "other-ws", "target-ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-unrelated", other["seaweedfs-s3-key"])
}

func TestSnapshotSecretsRejectMalformedWithoutDisclosingValues(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"key":null}`, `{"key":17}`, `{"": "sensitive-fixture"}`, `{"key":"sensitive-fixture","key":"second"}`, `{"key":{"data":"sensitive-fixture"}}`, `{"key":"sensitive-fixture"} {}`} {
		_, err := decodeSnapshotSecrets([]byte(raw))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive-fixture")
	}
}

func snapshotFixtureZip(t *testing.T, secrets *string) string {
	t.Helper()
	dir := t.TempDir()
	meta, _ := json.Marshal(NamespaceSnapshotMeta{Volumes: []VolumeSnapshotMeta{{Name: "postgres", DataFile: "postgres.tar.zst"}}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, metaFileName), meta, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "postgres.tar.zst"), []byte("fixture"), 0o600))
	if secrets != nil {
		require.NoError(t, os.WriteFile(filepath.Join(dir, secretsFileName), []byte(*secrets), 0o600))
	}
	path := filepath.Join(t.TempDir(), "snapshot.zip")
	require.NoError(t, createZip(path, dir))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	return path
}

func TestSnapshotImportSecretsPreflightBeforeVolumes(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "locked-vault"}[locked], func(t *testing.T) {
			svc, store := snapshotVault(t, "target-vault-fixture")
			raw := `{"key":null}`
			if locked {
				raw = `{"key":"fixture"}`
				// A second service opens the encrypted vault without its key.
				svc, _ = storage.NewSecretService(store)
			}
			archive := snapshotFixtureZip(t, &raw)
			base := t.TempDir()
			vol := filepath.Join(base, "volumes", "postgres")
			require.NoError(t, os.MkdirAll(vol, 0o700))
			marker := filepath.Join(vol, "original")
			require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
			dc := &fakeVolumeOps{}
			_, err := Import(context.Background(), dc, archive, base, NamespaceSecrets(svc, "target-ws", "target-ns"), nil)
			require.Error(t, err)
			require.FileExists(t, marker)
			require.Nil(t, dc.lastCmd)
		})
	}
}

func TestLegacySnapshotKeepsSecrets(t *testing.T) {
	svc, _ := snapshotVault(t, "vault-fixture")
	putNamespaceSecret(t, svc, "ws", "ns", "key", "fixture-existing")
	_, err := Import(context.Background(), &fakeVolumeOps{}, snapshotFixtureZip(t, nil), t.TempDir(), NamespaceSecrets(svc, "ws", "ns"), nil)
	require.NoError(t, err)
	values, err := NamespaceSecrets(svc, "ws", "ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-existing", values["key"])
}

type failingSecretService struct {
	*storage.SecretService
	remaining int
}

func (s *failingSecretService) SaveSecret(row storage.Secret) error {
	if s.remaining == 0 {
		s.remaining--
		return errors.New("fixture write failure")
	}
	s.remaining--
	if err := s.SecretService.SaveSecret(row); err != nil {
		return fmt.Errorf("fixture save: %w", err)
	}
	return nil
}
func TestSnapshotSecretsRollBackFailedRestore(t *testing.T) {
	svc, _ := snapshotVault(t, "vault-fixture")
	putNamespaceSecret(t, svc, "ws", "ns", "a", "fixture-original")
	provider := &namespaceSecrets{svc: &failingSecretService{SecretService: svc, remaining: 1}, workspaceID: "ws", namespaceID: "ns"}
	restore, err := provider.PrepareRestore(map[string]string{"a": "fixture-updated", "b": "fixture-new"})
	require.NoError(t, err)
	require.Error(t, restore())
	got, err := NamespaceSecrets(svc, "ws", "ns").Read()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "fixture-original", got["a"])
}

// exportVolume writes through the utils bind; this fake exercises the actual
// ZIP writer and its secrets entry without replacing unrelated Docker workloads.
type snapshotArchiveOps struct{ fakeVolumeOps }

func (s *snapshotArchiveOps) RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error) {
	if strings.Contains(cmd[2], "stat -c") {
		dir := strings.TrimSuffix(binds[1], ":/dest")
		err := os.WriteFile(filepath.Join(dir, "postgres.tar.zst"), []byte("fixture"), 0o600)
		if err != nil {
			return "", 0, fmt.Errorf("fixture archive: %w", err)
		}
		return "0:0|0700", 0, nil
	}
	return s.fakeVolumeOps.RunUtilsContainer(ctx, cmd, binds)
}
func TestSnapshotArchiveExportsAndRestoresSecrets(t *testing.T) {
	source, _ := snapshotVault(t, "source-vault-fixture")
	putNamespaceSecret(t, source, "source-ws", "source-ns", "seaweedfs-s3-key", "fixture-source")
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres"), 0o700))
	archive := filepath.Join(t.TempDir(), "snapshot.zip")
	_, err := Export(context.Background(), &snapshotArchiveOps{}, archive, base, nil, NamespaceSecrets(source, "source-ws", "source-ns"), nil, nil)
	require.NoError(t, err)
	reader, err := zip.OpenReader(archive)
	require.NoError(t, err)
	defer reader.Close()
	found := false
	for _, file := range reader.File {
		if file.Name == secretsFileName {
			found = true
			require.Equal(t, os.FileMode(0o600), file.Mode().Perm())
		}
	}
	require.True(t, found)
	target, _ := snapshotVault(t, "target-vault-fixture")
	_, err = Import(context.Background(), &fakeVolumeOps{}, archive, t.TempDir(), NamespaceSecrets(target, "target-ws", "target-ns"), nil)
	require.NoError(t, err)
	got, err := NamespaceSecrets(target, "target-ws", "target-ns").Read()
	require.NoError(t, err)
	require.Equal(t, "fixture-source", got["seaweedfs-s3-key"])
}

func TestPartialImportLeavesUnselectedVolumesInPlace(t *testing.T) {
	base := t.TempDir()
	keep := filepath.Join(base, "volumes", "seaweedfs2", "data")
	replace := filepath.Join(base, "volumes", "postgres", "old")
	for _, path := range []string{keep, replace} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("keep"), 0o600))
	}
	_, err := Import(context.Background(), &fakeVolumeOps{}, snapshotFixtureZip(t, nil), base, nil, nil)
	require.NoError(t, err)
	require.FileExists(t, keep)
	require.NoFileExists(t, replace)
	backups, err := filepath.Glob(filepath.Join(base, "volumes.bak-*", "postgres", "old"))
	require.NoError(t, err)
	require.Len(t, backups, 1)
}

func TestSnapshotExportWithoutSecretsDoesNotReadVault(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres"), 0o700))
	archive := filepath.Join(t.TempDir(), "snapshot.zip")
	_, err := Export(context.Background(), &snapshotArchiveOps{}, archive, base, nil, nil, nil, nil)
	require.NoError(t, err)
	reader, err := zip.OpenReader(archive)
	require.NoError(t, err)
	defer reader.Close()
	for _, file := range reader.File {
		require.NotEqual(t, secretsFileName, file.Name)
	}
}

func TestSnapshotSystemCredentialsPreflightAndReexport(t *testing.T) {
	svc, _ := snapshotVault(t, "system-fixture-vault")
	base := NamespaceSecrets(svc, "ws", "ns")
	name := systemsecrets.NamespaceName(systemsecrets.IDOIDC)
	_, err := base.PrepareRestore(map[string]string{name: ""})
	require.Error(t, err)
	_, err = base.PrepareRestore(map[string]string{"@citeck/system/unknown": "fixture"})
	require.Error(t, err)
	putNamespaceSecret(t, svc, "ws", "ns", name, "imported-fixture")
	values, err := WithSystemSecrets(base, map[string]string{name: "profile-fixture"}).Read()
	require.NoError(t, err)
	require.Equal(t, "imported-fixture", values[name])
}
