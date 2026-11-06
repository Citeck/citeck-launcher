package launchermcp

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PropsRoot pins each directory handle beneath a canonical, trusted runtime
// home. No untrusted parent symlink is accepted, including one pointing into
// another app's props directory. Handles survive concurrent directory renames.
func PropsRoot(base, app string) (*os.Root, error) {
	if app == "" || app == "." || app == ".." || strings.ContainsAny(app, "/\\") {
		return nil, errors.New("invalid app id")
	}
	root, err := fsutil.OpenCanonicalRoot(base)
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	defer root.Close()
	parent, _, err := fsutil.RootedParent(root, "app/"+app+"/props/application.yml", false)
	if err != nil {
		return nil, fmt.Errorf("open Spring props root: %w", err)
	}
	return parent, nil
}
func validLeaf(name string) bool {
	return name == filepath.Base(name) && !strings.ContainsAny(name, "/\\") && strings.HasPrefix(name, "application") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
}

// OpenRegular verifies the opened object's identity, not just a preceding path
// check. os.Root itself enforces containment across path swaps. Nonblocking
// open prevents a concurrently substituted FIFO from hanging before Stat.
func OpenRegular(root *os.Root, name string) (*os.File, error) {
	if !validLeaf(name) {
		return nil, errors.New("ineligible Spring YAML name")
	}
	f, err := fsutil.OpenRegularRooted(root, name)
	if err != nil {
		return nil, fmt.Errorf("open Spring YAML: %w", err)
	}
	return f, nil
}

// ReadYAML refuses oversized documents without returning a partial editable result.
func ReadYAML(root *os.Root, name string) ([]byte, error) {
	f, err := OpenRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	if len(b) > MaxBytes {
		return nil, errors.New("spring YAML exceeds 256 KiB; document not returned")
	}
	return b, nil
}

// WriteYAML never opens the destination for writing: rename replaces the leaf,
// so even a post-validation symlink cannot redirect a write. Temp creation,
// sync, rename and cleanup are all relative to the pinned props root.
func WriteYAML(root *os.Root, name string, b []byte) error {
	if len(b) > MaxBytes {
		return errors.New("spring YAML exceeds 256 KiB")
	}
	f, err := OpenRegular(root, name)
	if err != nil {
		return err
	}
	_ = f.Close()
	if err := fsutil.AtomicWriteRooted(root, name, b, 0o644); err != nil {
		return fmt.Errorf("write Spring YAML: %w", err)
	}
	return nil
}
