package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func runtimeFileMode(name string) os.FileMode {
	if strings.HasSuffix(name, ".sh") {
		return 0o755
	}
	return 0o644
}

// prepareDestPath operates on a pinned parent, not an absolute pathname.
// Comparison is bounded by the known generated content length. Chmod uses the
// opened descriptor, so a replacement symlink cannot redirect it.
func prepareDestPath(parent *os.Root, leaf string, content []byte) (bool, error) {
	info, err := parent.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runtime files: %w", err)
	}
	if info.IsDir() {
		if operationErr := parent.RemoveAll(leaf); operationErr != nil {
			return false, fmt.Errorf("runtime files: %w", operationErr)
		}
		return false, nil
	}
	f, err := fsutil.OpenRegularRooted(parent, leaf)
	if err != nil {
		return false, fmt.Errorf("open generated file: %w", err)
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return false, fmt.Errorf("runtime files: %w", err)
	}
	if info.Size() != int64(len(content)) {
		return false, nil
	}
	b, err := readKnownFile(f, int64(len(content)))
	if err != nil {
		return false, err
	}
	if !bytes.Equal(b, content) {
		return false, nil
	}
	if info.Mode().Perm() != runtimeFileMode(leaf) {
		if operationErr := f.Chmod(runtimeFileMode(leaf)); operationErr != nil {
			return false, fmt.Errorf("restore generated file mode: %w", operationErr)
		}
	}
	return true, nil
}

// writeRuntimeFiles returns materialization errors before definitions or hashes
// can be handed to Runtime. Already written other files need not be rolled back.
func writeRuntimeFiles(base string, files map[string][]byte, edited map[string]bool) error {
	if operationErr := os.MkdirAll(base, 0o755); operationErr != nil { //nolint:gosec // G301: runtime bind directories must be accessible to container users.
		return fmt.Errorf("runtime files: %w", operationErr)
	}
	root, err := fsutil.OpenCanonicalRoot(base)
	if err != nil {
		return fmt.Errorf("runtime files: %w", err)
	}
	defer root.Close()
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if edited[key] {
			continue
		}
		parent, leaf, e := fsutil.RootedParent(root, key, true)
		if e != nil {
			return fmt.Errorf("materialize %s: %w", key, e)
		}
		skip, e := prepareDestPath(parent, leaf, files[key])
		if e == nil && !skip {
			e = fsutil.AtomicWriteRooted(parent, leaf, files[key], runtimeFileMode(leaf))
		}
		_ = parent.Close()
		if e != nil {
			return fmt.Errorf("materialize %s: %w", key, e)
		}
	}
	return nil
}

// Missing, unsafe and oversized overlays stay ABSENT, distinct from a present
// zero-byte document. Merge may replay the persisted delta without current;
// when a conflict requires current it returns an error, preserving disk+edit.
func readDiskContent(base string, edits map[string]namespace.FileEdit) map[string][]byte {
	out := make(map[string][]byte, len(edits))
	if len(edits) == 0 {
		return out
	}
	root, err := fsutil.OpenCanonicalRoot(base)
	if err != nil {
		slog.Warn("Runtime overlays unavailable", "err", err)
		return out
	}
	defer root.Close()
	for key := range edits {
		parent, leaf, e := fsutil.RootedParent(root, key, false)
		if e != nil {
			slog.Warn("Runtime overlay unavailable", "key", key, "err", e)
			continue
		}
		b, e := fsutil.ReadRooted(parent, leaf, 1<<20)
		_ = parent.Close()
		if e != nil {
			slog.Warn("Runtime overlay unavailable", "key", key, "err", e)
			continue
		}
		out[key] = b
	}
	return out
}
func readKnownFile(f *os.File, n int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, n+1))
	if err != nil {
		return nil, fmt.Errorf("read generated comparison: %w", err)
	}
	return data, nil
}
