package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OpenCanonicalRoot allows a trusted installation home to be a symlink. All
// untrusted descendants must subsequently go through RootedParent.
func OpenCanonicalRoot(base string) (*os.Root, error) {
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, fmt.Errorf("rooted: %w", err)
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("open canonical root: %w", err)
	}
	return root, nil
}

// RootedParent pins directory handles one component at a time, rejecting
// symlinks even when they point elsewhere inside the runtime tree. Never use
// the resulting Root.Name as a pathname for I/O: directories can be renamed.
func RootedParent(root *os.Root, key string, create bool) (*os.Root, string, error) {
	if !fs.ValidPath(key) || key == "." || strings.ContainsAny(key, "\\:") {
		return nil, "", errors.New("invalid relative runtime key")
	}
	parts := strings.Split(key, "/")
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, "", fmt.Errorf("rooted: %w", err)
	}
	for _, part := range parts[:len(parts)-1] {
		before, e := current.Lstat(part)
		if errors.Is(e, os.ErrNotExist) && create {
			e = current.Mkdir(part, 0o755)
			if e == nil || errors.Is(e, os.ErrExist) {
				before, e = current.Lstat(part)
			}
		}
		if e != nil {
			_ = current.Close()
			return nil, "", fmt.Errorf("rooted: %w", e)
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			_ = current.Close()
			return nil, "", errors.New("runtime parent is not a regular directory")
		}
		next, e := current.OpenRoot(part)
		if e != nil {
			_ = current.Close()
			return nil, "", fmt.Errorf("rooted: %w", e)
		}
		actual, e := next.Stat(".")
		_ = current.Close()
		if e != nil || !os.SameFile(before, actual) {
			_ = next.Close()
			return nil, "", errors.New("runtime parent changed while opening")
		}
		current = next
	}
	return current, parts[len(parts)-1], nil
}

// OpenRegularRooted uses a no-follow, nonblocking actual open and then checks
// the opened handle. A path check alone cannot prevent a FIFO/symlink swap.
func OpenRegularRooted(root *os.Root, leaf string) (*os.File, error) {
	if leaf == "" || leaf == "." || leaf == ".." || strings.ContainsAny(leaf, "/\\:") {
		return nil, errors.New("invalid file leaf")
	}
	f, err := openRootedNoFollow(root, leaf)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("runtime file is not a regular non-symlink file")
	}
	return f, nil
}

// ReadRooted refuses oversize content instead of returning a partial editable file.
func ReadRooted(root *os.Root, leaf string, limit int64) ([]byte, error) {
	f, err := OpenRegularRooted(root, leaf)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("rooted: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("runtime file exceeds %d bytes", limit)
	}
	return b, nil
}

// AtomicWriteRooted replaces a leaf, never follows it. Parent, temp creation,
// mode setting, sync, rename and cleanup all use pinned handles.
func AtomicWriteRooted(root *os.Root, leaf string, data []byte, mode os.FileMode) error {
	if leaf == "" || leaf == "." || leaf == ".." || strings.ContainsAny(leaf, "/\\:") {
		return errors.New("invalid file leaf")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("rooted: %w", err)
	}
	temp := ".atomic-" + hex.EncodeToString(nonce)
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("rooted: %w", err)
	}
	defer root.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("rooted: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("rooted: %w", closeErr)
	}
	if err := root.Rename(temp, leaf); err != nil {
		return fmt.Errorf("replace rooted file: %w", err)
	}
	return nil
}
