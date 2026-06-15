package fsutil

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
}

func TestExtractZip_StripsSingleRootDir(t *testing.T) {
	// Create a zip with a single root dir (GitHub pattern: repo-main/...)
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	createTestZip(t, zipPath, map[string]string{
		"launcher-workspace-main/workspace-v1.yml":      "imageRepos: []\n",
		"launcher-workspace-main/community/2025.12.yml": "key: 2025.12\n",
		"launcher-workspace-main/community/2026.1.yml":  "key: 2026.1\n",
	})

	destDir := filepath.Join(t.TempDir(), "repo")
	count, err := ExtractZip(zipPath, destDir, WithStripSingleRootDir())
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Files should be at root level (prefix stripped)
	assert.FileExists(t, filepath.Join(destDir, "workspace-v1.yml"))
	assert.FileExists(t, filepath.Join(destDir, "community", "2025.12.yml"))
	assert.FileExists(t, filepath.Join(destDir, "community", "2026.1.yml"))
}

func TestExtractZip_NoRootDir(t *testing.T) {
	// Create a zip without a single root dir (files at top level)
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	createTestZip(t, zipPath, map[string]string{
		"workspace-v1.yml":      "imageRepos: []\n",
		"community/2025.12.yml": "key: 2025.12\n",
	})

	destDir := filepath.Join(t.TempDir(), "repo")
	count, err := ExtractZip(zipPath, destDir, WithStripSingleRootDir())
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	assert.FileExists(t, filepath.Join(destDir, "workspace-v1.yml"))
	assert.FileExists(t, filepath.Join(destDir, "community", "2025.12.yml"))
}

func TestExtractZip_WithoutStripOption_KeepsRootDir(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	createTestZip(t, zipPath, map[string]string{
		"repo-main/a.txt": "a\n",
	})

	destDir := filepath.Join(t.TempDir(), "out")
	count, err := ExtractZip(zipPath, destDir)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.FileExists(t, filepath.Join(destDir, "repo-main", "a.txt"))
}

func TestExtractZip_ZipSlipRejected(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	createTestZip(t, zipPath, map[string]string{
		"../../../etc/passwd": "root:x:0:0\n",
	})

	destDir := filepath.Join(t.TempDir(), "repo")
	_, err := ExtractZip(zipPath, destDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zip slip")

	// Nothing must have escaped destDir.
	assert.NoFileExists(t, filepath.Join(filepath.Dir(destDir), "etc", "passwd"))
}

func TestExtractZip_PreservesContent(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	createTestZip(t, zipPath, map[string]string{
		"file1.txt":        "hello",
		"subdir/file2.txt": "world",
	})

	destDir := t.TempDir()
	count, err := ExtractZip(zipPath, destDir)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	got1, err := os.ReadFile(filepath.Join(destDir, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got1))

	got2, err := os.ReadFile(filepath.Join(destDir, "subdir", "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(got2))
}

func TestExtractZip_MissingArchive(t *testing.T) {
	_, err := ExtractZip(filepath.Join(t.TempDir(), "nope.zip"), t.TempDir())
	require.Error(t, err)
}

func TestDetectSingleRootDir(t *testing.T) {
	tests := []struct {
		name   string
		names  []string
		expect string
	}{
		{"github pattern", []string{"repo-main/a.txt", "repo-main/b/c.txt"}, "repo-main/"},
		{"no root", []string{"a.txt", "b.txt"}, ""},
		{"multiple roots", []string{"dir1/a.txt", "dir2/b.txt"}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := make([]*zip.File, len(tt.names))
			for i, n := range tt.names {
				files[i] = &zip.File{FileHeader: zip.FileHeader{Name: n}}
			}
			assert.Equal(t, tt.expect, detectSingleRootDir(files))
		})
	}
}
