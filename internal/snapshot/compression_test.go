package snapshot

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSnapshotExportDefaultsToXZ(t *testing.T) {
	config.SetDesktopMode(false)
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres"), 0o700))
	archive := filepath.Join(t.TempDir(), "snapshot.zip")
	ops := &snapshotArchiveOps{}
	meta, err := Export(context.Background(), ops, archive, base, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, meta.Volumes, 1)
	require.Equal(t, "postgres.tar.xz", meta.Volumes[0].DataFile)
	require.Contains(t, ops.lastCmd[2], `tar --xz -cf "/dest/postgres.tar.xz"`)
	reader, err := zip.OpenReader(archive)
	require.NoError(t, err)
	defer reader.Close()
	var stored NamespaceSnapshotMeta
	for _, file := range reader.File {
		if file.Name == metaFileName {
			input, openErr := file.Open()
			require.NoError(t, openErr)
			require.NoError(t, json.NewDecoder(input).Decode(&stored))
			require.NoError(t, input.Close())
		}
	}
	require.Equal(t, meta.Volumes, stored.Volumes)
	file, err := reader.Open("postgres.tar.xz")
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func TestSnapshotImportRetainsXZAndZstandardSupport(t *testing.T) {
	config.SetDesktopMode(false)
	for _, codec := range []struct{ ext, flag string }{{"xz", "--xz"}, {"zst", "--zstd"}} {
		t.Run(codec.ext, func(t *testing.T) {
			dir := t.TempDir()
			name := "postgres.tar." + codec.ext
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o600))
			ops := &fakeVolumeOps{}
			require.NoError(t, checkVolumeArchive(context.Background(), ops, path))
			require.Contains(t, ops.lastCmd[2], "tar "+codec.flag+" -tf")
			vol := VolumeSnapshotMeta{Name: "postgres", DataFile: name}
			require.NoError(t, validateVolumeSnapshotMeta(vol))
			require.NoError(t, importVolume(context.Background(), ops, vol, path, dir))
			require.Contains(t, ops.lastCmd[2], "tar "+codec.flag+" -xf")
		})
	}
}
