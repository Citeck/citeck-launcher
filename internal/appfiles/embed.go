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
// Files are overwritten if their content has changed (detected by size comparison).
func ExtractTo(targetDir string) error {
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(targetDir, path), 0o755) //nolint:gosec // G301: directories need 0o755 for Docker bind-mount access
		}

		data, readErr := files.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read embedded file %s: %w", path, readErr)
		}

		destPath := filepath.Join(targetDir, path)
		perm := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			perm = 0o755
		}

		// If a directory exists at the file path (stale Docker bind mount artifact), remove it
		if fi, statErr := os.Stat(destPath); statErr == nil {
			if fi.IsDir() {
				_ = os.RemoveAll(destPath)
			} else if fi.Size() == int64(len(data)) {
				// Same size — likely unchanged, just fix permissions if needed
				if strings.HasSuffix(path, ".sh") && fi.Mode().Perm() != 0o755 {
					_ = os.Chmod(destPath, perm) //nolint:gosec // G302: shell scripts need 0o755
				}
				return nil
			}
			// Size differs → file was updated in new version, overwrite below
		}

		if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o755); mkdirErr != nil { //nolint:gosec // G301: directories need 0o755 for Docker bind-mount access
			return fmt.Errorf("create parent dir for %s: %w", destPath, mkdirErr)
		}

		if writeErr := os.WriteFile(destPath, data, perm); writeErr != nil {
			return fmt.Errorf("write file %s: %w", destPath, writeErr)
		}
		// Explicit chmod — WriteFile respects umask which may strip execute bit
		return os.Chmod(destPath, perm)
	})
	if err != nil {
		return fmt.Errorf("extract appfiles: %w", err)
	}
	return nil
}

// GetFiles returns all embedded files as a map of path -> content.
func GetFiles() (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := files.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read embedded %s: %w", path, readErr)
		}
		result[path] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk appfiles: %w", err)
	}
	return result, nil
}
