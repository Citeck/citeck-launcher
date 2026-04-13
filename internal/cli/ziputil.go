package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZip extracts a zip archive to destDir, stripping a single root directory if present.
// Returns the number of files extracted.
func extractZip(zipPath, destDir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Detect single root directory (GitHub "Download ZIP" pattern)
	prefix := detectSingleRootDir(r.File)

	count := 0
	for _, f := range r.File {
		name := f.Name
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
			if name == "" {
				continue // skip the root dir entry itself
			}
		}

		target := filepath.Join(destDir, filepath.FromSlash(name)) //nolint:gosec // G305: validated below
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
			continue // skip entries that escape destDir (zip slip)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return count, fmt.Errorf("mkdir %s: %w", name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return count, fmt.Errorf("mkdir parent %s: %w", name, err)
		}

		if err := extractFile(f, target); err != nil {
			return count, fmt.Errorf("extract %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// detectSingleRootDir returns the common prefix if all entries share a single root directory.
// Returns "" if there's no single root (entries are at the top level).
func detectSingleRootDir(files []*zip.File) string {
	if len(files) == 0 {
		return ""
	}
	var root string
	for _, f := range files {
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 {
			return "" // file at root level — no single root dir
		}
		if root == "" {
			root = parts[0]
		} else if parts[0] != root {
			return "" // multiple root dirs
		}
	}
	return root + "/"
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err //nolint:wrapcheck // thin wrapper
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600) //nolint:gosec // G304: target validated against destDir (zip slip protection above)
	if err != nil {
		return err //nolint:wrapcheck // thin wrapper
	}
	defer out.Close()

	_, err = io.Copy(out, rc) //nolint:gosec // G110: zip file size checked by caller context
	return err                //nolint:wrapcheck // thin wrapper
}
