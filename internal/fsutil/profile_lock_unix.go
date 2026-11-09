//go:build !windows

package fsutil

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openProfileLock(root *os.Root) (*os.File, error) {
	f, err := root.OpenFile(".daemon-recovery.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open profile lock entry: %w", err)
	}
	return f, nil
}
func lockProfileFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrProfileBusy
	}
	if err != nil {
		return fmt.Errorf("lock launcher profile: %w", err)
	}
	return nil
}
