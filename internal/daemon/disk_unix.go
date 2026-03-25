//go:build !windows

package daemon

import (
	"fmt"
	"syscall"
)

// diskSpace returns free and total disk space in GB for the given path.
func diskSpace(path string) (freeGB, totalGB float64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	if stat.Bsize <= 0 {
		return 0, 0, fmt.Errorf("invalid block size: %d", stat.Bsize)
	}
	bsize := uint64(stat.Bsize)
	freeGB = float64(stat.Bavail*bsize) / (1 << 30)
	totalGB = float64(stat.Blocks*bsize) / (1 << 30)
	return freeGB, totalGB, nil
}
