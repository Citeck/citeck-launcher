//go:build !windows

package fsutil

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func openRootedNoFollow(root *os.Root, leaf string) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("rooted unix: %w", err)
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0) //nolint:gosec // G115: Fd is an OS-created Unix descriptor, not an arbitrary uintptr.
	if err != nil {
		return nil, fmt.Errorf("rooted unix: %w", err)
	}
	return os.NewFile(uintptr(fd), leaf), nil //nolint:gosec // G115: Openat succeeded, so fd is a nonnegative OS descriptor.
}
