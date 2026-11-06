package launchermcp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// Pin the private parent before operating on its leaf. Identity checks bridge
// the platform permission helpers and rooted I/O; no checked pathname is used
// subsequently for reading, temporary-file creation or replacement.
func openPrivateStateRoot(path string) (*os.Root, string, error) {
	dir := filepath.Dir(path)
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, "", fmt.Errorf("MCP private parent: %w", err)
	}
	if permissionErr := privatePermissions(dir, true); permissionErr != nil {
		return nil, "", permissionErr
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("MCP private root: %w", err)
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(before, actual) {
		_ = root.Close()
		return nil, "", errors.New("MCP private parent changed while opening")
	}
	return root, filepath.Base(path), nil
}

func readPrivateState(path string) ([]byte, error) {
	root, leaf, err := openPrivateStateRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Lstat(leaf)
	if err != nil {
		return nil, fmt.Errorf("MCP private leaf: %w", err)
	}
	if permissionErr := privatePermissions(path, false); permissionErr != nil {
		return nil, permissionErr
	}
	f, err := fsutil.OpenRegularRooted(root, leaf)
	if err != nil {
		return nil, fmt.Errorf("MCP private read: %w", err)
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return nil, errors.New("MCP private leaf changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("MCP private read: %w", err)
	}
	if len(b) > 1<<20 {
		return nil, errors.New("MCP state too large")
	}
	return b, nil
}
