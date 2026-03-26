package snapshot

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		{Name: "", DataFile: "ok.tar.zst"},                             // empty name
		{Name: "ok", DataFile: ""},                                     // empty dataFile
		{Name: "../escape", DataFile: "ok.tar.zst"},                    // path traversal in name
		{Name: "ok", DataFile: "../escape.tar.zst"},                    // path traversal in dataFile
		{Name: "ok", DataFile: `"; echo PWNED; ".tar.zst`},            // shell injection in dataFile
		{Name: "sub/dir", DataFile: "ok.tar.zst"},                     // path separator in name
		{Name: "ok", DataFile: "has spaces.tar.zst"},                  // unsafe chars in dataFile
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
	err = extractZip(zipPath, destDir)
	if err == nil {
		t.Fatal("extractZip should reject zip-slip path traversal")
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
	if err := extractZip(zipPath, destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
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
