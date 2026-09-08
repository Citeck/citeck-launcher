//go:build darwin

package fsutil

import "syscall"

// AvailableDiskSpace returns available bytes at the given path, or 0 if unknown.
func AvailableDiskSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize) //nolint:gosec // overflow not possible for filesystem block counts
}
