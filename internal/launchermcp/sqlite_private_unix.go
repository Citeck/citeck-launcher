//go:build !windows

package launchermcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Other users must not be able to replace a VFS pathname component. Trusted
// ancestor symlinks (e.g. /var on macOS) are resolved before fixing the parent.
// Same-user/root compromise and filesystems that lie about fsync are not covered.
func trustedDBAncestor(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", mcpStorageError(err)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", mcpStorageError(err)
	}
	current := string(filepath.Separator)
	for part := range strings.SplitSeq(strings.TrimPrefix(p, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			return "", mcpStorageError(e)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (st.Uid != 0 && int64(st.Uid) != int64(os.Geteuid())) {
			return "", errors.New("MCP has an untrusted storage ancestor")
		}
		if info.Mode().Perm()&0o022 != 0 && (info.Mode()&os.ModeSticky == 0 || st.Uid != 0) {
			return "", errors.New("MCP storage ancestor can be replaced by another user")
		}
	}
	return p, nil
}
func prepareDBDirectory(raw string) (string, error) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", mcpStorageError(err)
	}
	ancestor := filepath.Dir(abs)
	if err = os.MkdirAll(ancestor, 0o700); err != nil {
		return "", mcpStorageError(err)
	}
	parent, err := trustedDBAncestor(ancestor)
	if err != nil {
		return "", mcpStorageError(err)
	}
	dir := filepath.Join(parent, filepath.Base(abs))
	if err = os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", mcpStorageError(err)
	}
	if err = privatePermissions(dir, true); err != nil {
		return "", mcpStorageError(err)
	}
	if err = syncDBDirectory(parent); err != nil {
		return "", mcpStorageError(err)
	}
	return dir, nil
}
func lockDBOwner(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("MCP owner lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path) //nolint:gosec // Successful syscall.Open returned a nonnegative file descriptor.
	if err = privatePermissions(path, false); err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		return nil, errors.Join(errors.New("MCP database is already owned or its lock is unsafe"), err, f.Close())
	}
	return f, nil
}
func syncDBDirectory(path string) error {
	f, err := os.Open(path) //nolint:gosec // Canonical private directory, never untrusted input.
	if err != nil {
		return mcpStorageError(err)
	}
	return errors.Join(f.Sync(), f.Close())
}

func replaceSQLMarker(root *os.Root, _, name string) error {
	return mcpStorageError(root.Rename(name, "state.json"))
}
