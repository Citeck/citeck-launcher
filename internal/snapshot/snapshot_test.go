package snapshot

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/stretchr/testify/require"
)

// fakeVolumeOps is a test double for volumeOps that records calls instead of
// touching Docker, so we can assert WHERE snapshot import/export targets data.
type fakeVolumeOps struct {
	scopedName        string
	createVolumeCalls []string
	listVolumes       []docker.VolumeInfo
	lastBinds         []string
	lastCmd           []string
}

func (f *fakeVolumeOps) CreateVolume(_ context.Context, orig string) (string, error) {
	f.createVolumeCalls = append(f.createVolumeCalls, orig)
	if f.scopedName != "" {
		return f.scopedName, nil
	}
	return "scoped_" + orig, nil
}
func (f *fakeVolumeOps) ListVolumes(_ context.Context) ([]docker.VolumeInfo, error) {
	return f.listVolumes, nil
}
func (f *fakeVolumeOps) RunUtilsContainer(_ context.Context, cmd, binds []string) (output string, exitCode int, err error) {
	f.lastCmd = cmd
	f.lastBinds = binds
	return "", 0, nil
}
func (f *fakeVolumeOps) ImageExists(_ context.Context, _ string) bool { return true }
func (f *fakeVolumeOps) PullImage(_ context.Context, _ string, _ *docker.RegistryAuth) error {
	return nil
}

func TestImportVolume_DesktopTargetsNamedVolume(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(func() { config.SetDesktopMode(false) })

	tmp := t.TempDir()
	tarPath := filepath.Join(tmp, "postgres2.tar.zst")
	if err := os.WriteFile(tarPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeVolumeOps{scopedName: "citeck_volume_postgres2_ns_ws"}
	vol := VolumeSnapshotMeta{Name: "postgres2", DataFile: "postgres2.tar.zst", RootStat: "999:999|0755"}

	err := importVolume(context.Background(), fake, vol, tarPath, filepath.Join(tmp, "rtfiles"))
	require.NoError(t, err)

	// Desktop containers mount the scoped named Docker volume, so import must
	// restore INTO it (not a bind-mount dir nobody mounts).
	require.Equal(t, []string{"postgres2"}, fake.createVolumeCalls)
	require.Contains(t, fake.lastBinds, "citeck_volume_postgres2_ns_ws:/dest")
	// The named volume persists across imports, so its stale contents must be
	// cleared before the restore (prevents mongo-style file mixing/corruption).
	require.Len(t, fake.lastCmd, 3)
	require.Contains(t, fake.lastCmd[2], "find /dest -mindepth 1 -delete")
}

func TestExportSources_DesktopUsesNamedVolumes(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(func() { config.SetDesktopMode(false) })

	fake := &fakeVolumeOps{listVolumes: []docker.VolumeInfo{
		{Name: "citeck_volume_postgres2_ns_ws", OrigName: "postgres2"},
		{Name: "citeck_volume_mongo_ns_ws", OrigName: "mongo"},
		{Name: "stray-volume", OrigName: ""}, // not launcher-managed → skipped
	}}

	srcs, err := exportSources(context.Background(), fake, "/ignored-in-desktop", nil)
	require.NoError(t, err)
	require.Len(t, srcs, 2)
	require.Equal(t, "postgres2", srcs[0].name)
	require.Equal(t, "citeck_volume_postgres2_ns_ws:/source:ro", srcs[0].sourceBind)
	require.Equal(t, "mongo", srcs[1].name)
	require.Equal(t, "citeck_volume_mongo_ns_ws:/source:ro", srcs[1].sourceBind)

	// include filter keys off the Docker volume Name (what the volume-list API
	// and the snapshot dialog expose), not the OrigName archive entry.
	filtered, err := exportSources(context.Background(), fake, "/ignored-in-desktop",
		map[string]bool{"citeck_volume_mongo_ns_ws": true})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "mongo", filtered[0].name)
}

func TestExportSources_ServerScansBindDir(t *testing.T) {
	config.SetDesktopMode(false)

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "volumes", "postgres2"), 0o755))

	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "volumes", "mongo"), 0o755))

	srcs, err := exportSources(context.Background(), &fakeVolumeOps{}, tmp, nil)
	require.NoError(t, err)
	require.Len(t, srcs, 2)
	require.Equal(t, "mongo", srcs[0].name)
	require.Equal(t, "postgres2", srcs[1].name)

	// include filter keys off the bind-dir name (what the volume-list API and
	// the snapshot dialog expose in server mode).
	filtered, err := exportSources(context.Background(), &fakeVolumeOps{}, tmp,
		map[string]bool{"postgres2": true})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "postgres2", filtered[0].name)
}

func TestImportVolume_ServerTargetsBindDir(t *testing.T) {
	config.SetDesktopMode(false)

	tmp := t.TempDir()
	tarPath := filepath.Join(tmp, "postgres2.tar.zst")
	if err := os.WriteFile(tarPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	volumesBase := filepath.Join(tmp, "rt")
	fake := &fakeVolumeOps{}
	vol := VolumeSnapshotMeta{Name: "postgres2", DataFile: "postgres2.tar.zst", RootStat: "999:999|0755"}

	err := importVolume(context.Background(), fake, vol, tarPath, volumesBase)
	require.NoError(t, err)

	require.Empty(t, fake.createVolumeCalls, "server mode must not create named volumes")
	require.Contains(t, fake.lastBinds, filepath.Join(volumesBase, "volumes", "postgres2")+":/dest")
	// Server mode restores into a fresh dir (Import backs up the old volumes dir),
	// so importVolume itself must NOT clear /dest.
	require.Len(t, fake.lastCmd, 3)
	require.NotContains(t, fake.lastCmd[2], "find /dest -mindepth 1 -delete")
}

func TestBackupServerVolumesDir(t *testing.T) {
	// Non-empty volumes dir is moved aside to volumes.bak-<ts>, leaving room for
	// a clean restore; the backup keeps the pre-import data.
	base := t.TempDir()
	volFile := filepath.Join(base, "volumes", "mongo2", "WiredTiger.wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(volFile), 0o755))
	require.NoError(t, os.WriteFile(volFile, []byte("data"), 0o644))

	require.NoError(t, backupServerVolumesDir(base))

	_, err := os.Stat(filepath.Join(base, "volumes"))
	require.True(t, os.IsNotExist(err), "volumes dir must be moved aside")

	baks, _ := filepath.Glob(filepath.Join(base, "volumes.bak-*", "mongo2", "WiredTiger.wt"))
	require.Len(t, baks, 1, "backup must preserve the original volume data")

	// Empty/missing volumes dir is a no-op (no backup created).
	empty := t.TempDir()
	require.NoError(t, backupServerVolumesDir(empty))
	none, _ := filepath.Glob(filepath.Join(empty, "volumes.bak-*"))
	require.Empty(t, none)
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"postgres", "postgres"},
		{"my-volume", "my-volume"},
		{"foo bar/baz", "foo_bar_baz"},
		{"a@b#c$d", "a_b_c_d"},
		{"normal.name-1", "normal.name-1"},
	}
	for _, tt := range tests {
		got := sanitizeFileName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsValidChown(t *testing.T) {
	valid := []string{"0:0", "999:999", "1000:1000", "65534:65534"}
	for _, s := range valid {
		if !isValidChown(s) {
			t.Errorf("isValidChown(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "abc", "0", ":0", "0:", "root:root", "0:0:0", "0;rm -rf /"}
	for _, s := range invalid {
		if isValidChown(s) {
			t.Errorf("isValidChown(%q) = true, want false", s)
		}
	}
}

func TestIsValidChmod(t *testing.T) {
	valid := []string{"755", "0755", "0644", "777", "0777", "0100"}
	for _, s := range valid {
		if !isValidChmod(s) {
			t.Errorf("isValidChmod(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "abc", "888", "0888", "999", "rwxrwxrwx", "0;rm", "99999"}
	for _, s := range invalid {
		if isValidChmod(s) {
			t.Errorf("isValidChmod(%q) = true, want false", s)
		}
	}
}

func TestValidateVolumeSnapshotMeta(t *testing.T) {
	valid := []VolumeSnapshotMeta{
		{Name: "postgres", DataFile: "postgres.tar.zst", RootStat: "999:999|0755"},
		{Name: "my-volume", DataFile: "my-volume.tar.xz", RootStat: "0:0|0755"},
	}
	for _, v := range valid {
		if err := validateVolumeSnapshotMeta(v); err != nil {
			t.Errorf("validateVolumeSnapshotMeta(%+v) = %v, want nil", v, err)
		}
	}

	invalid := []VolumeSnapshotMeta{
		{Name: "", DataFile: "ok.tar.zst"},                 // empty name
		{Name: "ok", DataFile: ""},                         // empty dataFile
		{Name: "../escape", DataFile: "ok.tar.zst"},        // path traversal in name
		{Name: "ok", DataFile: "../escape.tar.zst"},        // path traversal in dataFile
		{Name: "ok", DataFile: `"; echo PWNED; ".tar.zst`}, // shell injection in dataFile
		{Name: "sub/dir", DataFile: "ok.tar.zst"},          // path separator in name
		{Name: "ok", DataFile: "has spaces.tar.zst"},       // unsafe chars in dataFile
	}
	for _, v := range invalid {
		if err := validateVolumeSnapshotMeta(v); err == nil {
			t.Errorf("validateVolumeSnapshotMeta(%+v) = nil, want error", v)
		}
	}
}

func TestExtractZip_ZipSlipPrevention(t *testing.T) {
	// Create a ZIP with a path traversal entry
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	// This entry tries to escape the destination directory
	wr, err := w.Create("../../../etc/evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	wr.Write([]byte("malicious content"))
	w.Close()
	f.Close()

	destDir := t.TempDir()
	_, err = fsutil.ExtractZip(zipPath, destDir)
	if err == nil {
		t.Fatal("ExtractZip should reject zip-slip path traversal")
	}
	if !strings.Contains(err.Error(), "zip slip") {
		t.Errorf("error should mention 'zip slip', got: %v", err)
	}
}

func TestCreateZipAndExtractZip_RoundTrip(t *testing.T) {
	// Create source files
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(srcDir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("world"), 0o644)

	// Create ZIP
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	if err := createZip(zipPath, srcDir); err != nil {
		t.Fatalf("createZip failed: %v", err)
	}

	// Extract ZIP
	destDir := t.TempDir()
	if _, err := fsutil.ExtractZip(zipPath, destDir); err != nil {
		t.Fatalf("ExtractZip failed: %v", err)
	}

	// Verify contents
	got1, err := os.ReadFile(filepath.Join(destDir, "file1.txt"))
	if err != nil {
		t.Fatalf("read file1.txt: %v", err)
	}
	if string(got1) != "hello" {
		t.Errorf("file1.txt content = %q, want %q", got1, "hello")
	}

	got2, err := os.ReadFile(filepath.Join(destDir, "subdir", "file2.txt"))
	if err != nil {
		t.Fatalf("read subdir/file2.txt: %v", err)
	}
	if string(got2) != "world" {
		t.Errorf("subdir/file2.txt content = %q, want %q", got2, "world")
	}
}
