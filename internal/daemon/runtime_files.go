package daemon

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// Runtime bind-mount file materialization. writeRuntimeFiles is the single
// source of truth for everything under a namespace's rtfiles tree — the
// generator owns the content; nothing else writes into this directory.

// prepareDestPath handles the pre-write checks for a single runtime file:
//   - If the path is a directory (Docker auto-created it), remove it so we can
//     write a regular file in its place.
//   - If the path is an existing regular file with the same size and identical
//     contents, return skip=true so the caller can skip the write (avoids
//     touching the mtime and keeps the container deployment hash stable).
//
// destPath is always filepath.Join(baseDir, relPath) where baseDir is the
// trusted namespace runtime directory — the path is not user-supplied.
func prepareDestPath(destPath string, content []byte) (skip bool, err error) {
	fi, statErr := os.Stat(destPath)
	if statErr != nil {
		return false, nil // path does not exist yet — proceed with write
	}
	if fi.IsDir() {
		// Case 1: Docker auto-created a dir instead of a file; remove it.
		if err := os.RemoveAll(destPath); err != nil {
			return false, fmt.Errorf("remove dir at runtime file path: %w", err)
		}
		return false, nil
	}
	// Case 2: same size — compare bytes, skip if unchanged.
	if !fi.Mode().IsRegular() || int64(len(content)) != fi.Size() {
		return false, nil
	}
	existing, readErr := os.ReadFile(destPath) //nolint:gosec // G304: destPath is filepath.Join(trusted baseDir, relPath)
	if readErr != nil || !bytes.Equal(existing, content) {
		return false, nil
	}
	// Before skipping, make sure the mode still matches what we'd write —
	// a .sh that somehow lost its executable bit (umask change, prior
	// launcher version bug, manual edit) would otherwise never recover,
	// since we'd never rewrite the file.
	wantPerm := os.FileMode(0o644)
	if strings.HasSuffix(destPath, ".sh") {
		wantPerm = 0o755
	}
	if fi.Mode().Perm() != wantPerm {
		_ = os.Chmod(destPath, wantPerm)
	}
	return true, nil
}

// writeRuntimeFiles applies the generator's file map (the full set of
// files any app can bind-mount) to disk under baseDir. Single source of
// truth — nothing else writes into this directory tree. Handles three
// edge cases that the naïve loop-and-WriteFile version did not:
//
//  1. A host path exists as an EMPTY DIRECTORY where the container
//     expects a file. Docker auto-creates a directory whenever it needs
//     to bind-mount a path that doesn't exist on the host; if postgres
//     was recreated while its config was missing, we end up with
//     /opt/citeck/data/runtime/.../postgres/postgresql.conf as a dir
//     and postgres chokes with "configuration file contains errors".
//  2. Content is identical — skip the atomic-rename dance entirely, so
//     unchanged files don't get a new mtime each regenerate (preserves
//     the container deployment hash so Docker doesn't pointlessly
//     recreate containers whose files didn't really change).
//  3. Parent directory doesn't exist yet — MkdirAll first.
//
// Shell scripts (.sh) are written 0755, everything else 0644. The optional
// `edited` map (keys: "<app>/<rel-path>", no leading "./") flags files whose
// on-disk content was modified by the user through the Web UI; those entries
// are skipped so user edits survive reload/regenerate. Passing nil disables
// the skip behavior (initial materialization paths where the user-edit
// set has not yet been restored use nil).
func writeRuntimeFiles(baseDir string, files map[string][]byte, edited map[string]bool) {
	for filePath, content := range files {
		if edited[filePath] {
			slog.Debug("Skipping user-edited file", "path", filePath)
			continue
		}
		destPath := filepath.Join(baseDir, filePath)
		skip, prepErr := prepareDestPath(destPath, content)
		if prepErr != nil {
			slog.Error("Failed to remove stale dir at file path", "path", destPath, "err", prepErr)
			continue
		}
		if skip {
			continue
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o755); mkdirErr != nil { //nolint:gosec // G301: dirs need 0o755 for Docker bind-mount access
			slog.Error("Failed to create dir for generated file", "path", destPath, "err", mkdirErr)
			continue
		}
		perm := os.FileMode(0o644)
		if strings.HasSuffix(filePath, ".sh") {
			perm = 0o755
		}
		if writeErr := fsutil.AtomicWriteFile(destPath, content, perm); writeErr != nil {
			slog.Error("Failed to write generated file", "path", destPath, "err", writeErr)
			continue
		}
		// fsutil.AtomicWriteFile respects umask for the temp file; re-chmod
		// to the exact perm we want (matters for .sh which need 0755).
		if chmodErr := os.Chmod(destPath, perm); chmodErr != nil {
			slog.Warn("Failed to chmod generated file", "path", destPath, "err", chmodErr)
		}
	}
}

// readEditedFileOverlay reads on-disk content for every persisted user-edit
// key under volumesBase. Used at daemon startup before the runtime exists, so
// the first Generate call sees the user's edits in its VolumesContentHash
// input and recreates containers whose mounted files were changed in a prior
// session. Missing/unreadable files are skipped — writeRuntimeFiles will
// rematerialize the default the next time that key falls out of editedFiles.
func readEditedFileOverlay(volumesBase string, keys []string) map[string][]byte {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		abs := filepath.Join(volumesBase, k)
		data, err := os.ReadFile(abs) //nolint:gosec // G304: key is constrained to volumesBase by the original write
		if err != nil {
			continue
		}
		out[k] = data
	}
	return out
}
