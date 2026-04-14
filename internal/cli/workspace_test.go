package cli

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractZip_StripsSingleRootDir(t *testing.T) {
	// Create a zip with a single root dir (GitHub pattern: repo-main/...)
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	createTestZip(t, zipPath, map[string]string{
		"launcher-workspace-main/workspace-v1.yml":      "imageRepos: []\n",
		"launcher-workspace-main/community/2025.12.yml": "key: 2025.12\n",
		"launcher-workspace-main/community/2026.1.yml":  "key: 2026.1\n",
	})

	destDir := filepath.Join(t.TempDir(), "repo")
	count, err := extractZip(zipPath, destDir)
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
	count, err := extractZip(zipPath, destDir)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	assert.FileExists(t, filepath.Join(destDir, "workspace-v1.yml"))
	assert.FileExists(t, filepath.Join(destDir, "community", "2025.12.yml"))
}

func TestExtractZip_ZipSlipBlocked(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	createTestZip(t, zipPath, map[string]string{
		"../../../etc/passwd": "root:x:0:0\n",
		"normal.txt":          "ok\n",
	})

	destDir := filepath.Join(t.TempDir(), "repo")
	count, err := extractZip(zipPath, destDir)
	require.NoError(t, err)
	assert.Equal(t, 1, count) // only normal.txt extracted
	assert.FileExists(t, filepath.Join(destDir, "normal.txt"))
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
