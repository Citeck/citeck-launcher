package fsutil

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Zip-bomb protection limits for ExtractZip.
const (
	// maxZipExtractTotal is the aggregate extraction size limit (50 GB).
	maxZipExtractTotal int64 = 50 << 30
	// maxZipExtractFile is the per-entry extraction size limit (10 GB).
	maxZipExtractFile int64 = 10 << 30
)

type extractZipConfig struct {
	stripSingleRoot bool
}

// ExtractZipOption configures ExtractZip behavior.
type ExtractZipOption func(*extractZipConfig)

// WithStripSingleRootDir makes ExtractZip strip a single shared root
// directory from all entry names (the GitHub "Download ZIP" pattern:
// repo-main/...). No-op when entries don't share a single root.
func WithStripSingleRootDir() ExtractZipOption {
	return func(c *extractZipConfig) { c.stripSingleRoot = true }
}

// ExtractZip extracts a ZIP archive into destDir and returns the number of
// files extracted. Hardened against zip slip (path-traversal entries abort
// the extraction with an error) and zip bombs (per-entry and aggregate size
// caps). File modes from the archive are preserved (owner rw forced); parent
// directories are created as needed.
func ExtractZip(zipPath, destDir string, opts ...ExtractZipOption) (int, error) {
	var cfg extractZipConfig
	for _, o := range opts {
		o(&cfg)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	prefix := ""
	if cfg.stripSingleRoot {
		prefix = detectSingleRootDir(r.File)
	}

	cleanDest := filepath.Clean(destDir)
	var totalWritten int64
	count := 0

	for _, f := range r.File {
		name := f.Name
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
			if name == "" {
				continue // the root dir entry itself
			}
		}

		// Security: prevent zip slip.
		target := filepath.Join(cleanDest, filepath.FromSlash(name)) //nolint:gosec // G305: validated below
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return count, fmt.Errorf("zip slip detected: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil { //nolint:gosec // G301: extraction dirs need 0o755 (e.g. Docker volume access)
				return count, fmt.Errorf("mkdir %s: %w", name, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // G301: extraction dirs need 0o755 (e.g. Docker volume access)
			return count, fmt.Errorf("mkdir parent %s: %w", name, err)
		}

		remaining := maxZipExtractTotal - totalWritten
		if remaining <= 0 {
			return count, fmt.Errorf("zip extraction aborted: aggregate size exceeds %d GB", maxZipExtractTotal>>30)
		}
		n, err := extractZipEntry(f, target, min(remaining, maxZipExtractFile))
		totalWritten += n
		if err != nil {
			return count, fmt.Errorf("extract %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// extractZipEntry writes a single zip entry to target, enforcing the size
// limit. Returns the number of bytes written.
func extractZipEntry(f *zip.File, target string, limit int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("open entry: %w", err)
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600) //nolint:gosec // G304: target validated against destDir (zip-slip check in caller)
	if err != nil {
		return 0, fmt.Errorf("create: %w", err)
	}

	// Read limit+1 so an over-limit entry is detected (not silently truncated).
	n, err := io.Copy(out, io.LimitReader(rc, limit+1)) //nolint:gosec // G110: bounded by limit
	if err != nil {
		_ = out.Close()
		return n, fmt.Errorf("write: %w", err)
	}
	if n > limit {
		_ = out.Close()
		return n, fmt.Errorf("entry exceeds size limit (%d bytes)", limit)
	}
	if err := out.Close(); err != nil {
		return n, fmt.Errorf("close: %w", err)
	}
	return n, nil
}

// detectSingleRootDir returns the common "root/" prefix if all entries share
// a single root directory, or "" if entries live at the top level or under
// multiple roots.
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
