package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
)

// File discovery must use the same no-follow boundary as read/write. Calling
// the UI's pathname-based walker first and filtering afterwards is too late:
// it has already enumerated directories through untrusted intermediate links.
func (d *Daemon) mcpEditorFiles(a launchermcp.Arguments) (any, error) {
	if !d.longOp.TryLock(longOpRequest) {
		return nil, errors.New("LONG_OP_IN_PROGRESS")
	}
	defer d.longOp.Unlock()
	if !d.reloadMu.TryLock() {
		return nil, errors.New("RELOAD_IN_PROGRESS")
	}
	defer d.reloadMu.Unlock()
	act := d.active()
	def, ok := runtimeAppDef(act.runtime, a.App)
	if !ok {
		return nil, errors.New("APP_NOT_FOUND")
	}
	root, err := fsutil.OpenCanonicalRoot(act.volumesBase)
	if err != nil {
		return nil, fmt.Errorf("open application files: %w", err)
	}
	defer root.Close()
	keys := map[string]bool{}
	for _, volume := range def.Volumes {
		source, _, _ := strings.Cut(volume, ":")
		if !strings.HasPrefix(source, "./") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(source, "./"), "/")
		if key == "" {
			key = "."
		}
		if path.Clean(key) != key {
			continue
		}
		// A synthetic leaf asks RootedParent to pin every directory of a
		// directory mount (including the mount itself), without opening files.
		dir, _, openErr := fsutil.RootedParent(root, path.Join(key, ".mcp-list"), false)
		if openErr == nil {
			budget := 500
			err = mcpWalkEditorFiles(dir, key, 0, &budget, keys)
			_ = dir.Close()
			if err != nil {
				return nil, err
			}
			continue
		}
		// A regular-file mount, or an inaccessible/symlinked directory. Only
		// an actual no-follow regular-file open can admit a file to the list.
		parent, leaf, e := fsutil.RootedParent(root, key, false)
		if e != nil {
			continue
		}
		mcpAdmitEditorFile(parent, leaf, key, keys)
		_ = parent.Close()
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	slices.Sort(names)
	rows := make([]any, 0, len(names))
	for _, key := range names {
		rows = append(rows, map[string]any{"path": "./" + key, "edited": act.runtime.IsFileEdited(key)})
	}
	return rows, nil
}

func mcpAdmitEditorFile(root *os.Root, leaf, key string, keys map[string]bool) {
	if !mcpEditableFile(key) {
		return
	}
	f, err := fsutil.OpenRegularRooted(root, leaf)
	if err == nil {
		_ = f.Close()
		keys[key] = true
	}
}

func mcpWalkEditorFiles(root *os.Root, prefix string, depth int, budget *int, keys map[string]bool) error {
	if depth > 64 {
		return errors.New("application mount exceeds discovery depth limit (64)")
	}
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open application directory: %w", err)
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(64)
		for _, entry := range entries {
			*budget--
			if *budget < 0 {
				return errors.New("application mount exceeds discovery limit (500 entries)")
			}
			key := path.Join(prefix, entry.Name())
			if !entry.IsDir() {
				mcpAdmitEditorFile(root, entry.Name(), key, keys)
				continue
			}
			// RootedParent checks Lstat, opens a pinned directory handle and
			// verifies its identity; a directory-to-symlink swap is refused.
			child, _, e := fsutil.RootedParent(root, entry.Name()+"/.mcp-list", false)
			if e != nil {
				continue
			}
			e = mcpWalkEditorFiles(child, key, depth+1, budget, keys)
			_ = child.Close()
			if e != nil {
				return e
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("list application directory: %w", readErr)
		}
	}
}
