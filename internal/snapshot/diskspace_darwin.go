//go:build darwin

package snapshot

import "syscall"

// availableDiskSpace returns available bytes at the given path, or 0 if unknown.
func availableDiskSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize) //nolint:gosec // overflow not possible for filesystem block counts
}
