package appfiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTo_NewShFilesAreExecutable(t *testing.T) {
	targetDir := t.TempDir()

	require.NoError(t, ExtractTo(targetDir))

	// Walk the target to find all .sh files and verify they are executable
	shFiles := 0
	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) == ".sh" {
			shFiles++
			perm := info.Mode().Perm()
			assert.Equal(t, os.FileMode(0o755), perm,
				"file %s should have 0755 permissions, got %04o", path, perm)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, shFiles, 0, "should find at least one .sh file in embedded appfiles")
}

func TestExtractTo_ExistingShFileGetsChmoded(t *testing.T) {
	targetDir := t.TempDir()

	// First extract to create all files
	require.NoError(t, ExtractTo(targetDir))

	// Find an .sh file and downgrade its permissions to 0644
	var shPath string
	filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) == ".sh" && shPath == "" {
			shPath = path
		}
		return nil
	})
	require.NotEmpty(t, shPath, "need at least one .sh file")

	// Downgrade permissions — simulates older version writing as 0644
	require.NoError(t, os.Chmod(shPath, 0o644))

	// Verify it was actually downgraded
	info, err := os.Stat(shPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	// Run ExtractTo again — should fix the permissions
	require.NoError(t, ExtractTo(targetDir))

	info, err = os.Stat(shPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(),
		"ExtractTo should fix permissions on existing .sh files")
}

func TestExtractTo_NonShFilesAre0644(t *testing.T) {
	targetDir := t.TempDir()

	require.NoError(t, ExtractTo(targetDir))

	nonShFiles := 0
	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".sh" {
			nonShFiles++
			perm := info.Mode().Perm()
			assert.Equal(t, os.FileMode(0o644), perm,
				"non-.sh file %s should have 0644 permissions, got %04o", path, perm)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, nonShFiles, 0, "should find at least one non-.sh file")
}

func TestExtractTo_SkipsExistingRegularFiles(t *testing.T) {
	targetDir := t.TempDir()

	// First extraction
	require.NoError(t, ExtractTo(targetDir))

	// Get embedded files to find a non-.sh file
	embedded, err := GetFiles()
	require.NoError(t, err)

	var targetPath string
	var originalContent []byte
	for relPath, content := range embedded {
		if filepath.Ext(relPath) != ".sh" {
			targetPath = filepath.Join(targetDir, relPath)
			originalContent = content
			break
		}
	}
	require.NotEmpty(t, targetPath)

	// Overwrite with custom content
	customContent := []byte("custom-user-content")
	require.NoError(t, os.WriteFile(targetPath, customContent, 0o644))

	// Second extraction should NOT overwrite the existing file
	require.NoError(t, ExtractTo(targetDir))

	data, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, customContent, data,
		"ExtractTo should not overwrite existing regular files")
	assert.NotEqual(t, originalContent, data)
}

func TestExtractTo_RemovesStaleDirAtFilePath(t *testing.T) {
	targetDir := t.TempDir()

	// Get an embedded file path
	embedded, err := GetFiles()
	require.NoError(t, err)
	require.NotEmpty(t, embedded)

	var relPath string
	for p := range embedded {
		relPath = p
		break
	}

	// Create a directory where a file should go (stale Docker bind mount artifact)
	staleDir := filepath.Join(targetDir, relPath)
	require.NoError(t, os.MkdirAll(staleDir, 0o755))
	// Put a file inside so it's not empty
	require.NoError(t, os.WriteFile(filepath.Join(staleDir, "dummy"), []byte("x"), 0o644))

	// ExtractTo should remove the stale directory and replace with the file
	require.NoError(t, ExtractTo(targetDir))

	info, err := os.Stat(staleDir)
	require.NoError(t, err)
	assert.False(t, info.IsDir(), "stale directory should have been replaced with a file")
}

func TestGetFiles_ReturnsNonEmpty(t *testing.T) {
	files, err := GetFiles()
	require.NoError(t, err)
	assert.Greater(t, len(files), 0, "embedded files should not be empty")

	// Verify all files have non-empty content
	for path, content := range files {
		assert.Greater(t, len(content), 0, "file %s should have content", path)
	}
}
