//go:build !windows

package launchermcp

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func privatePermissions(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("private unix: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return errors.New("MCP private storage must not be a symlink or special file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 || !directory && stat.Nlink != 1 {
		return fmt.Errorf("MCP private storage %s must be owned by the daemon user and owner-only (mode %s)", path, info.Mode())
	}
	return nil
}
