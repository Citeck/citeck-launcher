package appfiles

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:postgres all:pgadmin all:keycloak all:proxy all:alfresco
var files embed.FS

// ExtractTo copies all embedded appfiles to the target directory.
// Files are only written if they don't already exist.
func ExtractTo(targetDir string) error {
	return fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(targetDir, path), 0o755)
		}

		destPath := filepath.Join(targetDir, path)

		// If a directory exists at the file path (stale Docker bind mount artifact), remove it
		if fi, statErr := os.Stat(destPath); statErr == nil {
			if fi.IsDir() {
				os.RemoveAll(destPath)
			} else {
				// Fix permissions on existing .sh files (may have been written as 0644 by older version)
				if strings.HasSuffix(path, ".sh") && fi.Mode().Perm() != 0o755 {
					os.Chmod(destPath, 0o755)
				}
				return nil // regular file already exists, skip
			}
		}

		data, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}

		perm := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			perm = 0o755
		}
		return os.WriteFile(destPath, data, perm)
	})
}

// GetFiles returns all embedded files as a map of path -> content.
func GetFiles() (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = data
		return nil
	})
	return result, err
}
