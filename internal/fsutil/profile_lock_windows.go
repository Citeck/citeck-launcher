package fsutil

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openProfileLock(root *os.Root) (*os.File, error) {
	f, err := root.OpenFile(".daemon-recovery.lock", os.O_CREATE|os.O_RDWR|openReparsePoint, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open profile lock entry: %w", err)
	}
	return f, nil
}
func lockProfileFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrProfileBusy
	}
	if err != nil {
		return fmt.Errorf("lock launcher profile: %w", err)
	}
	return nil
}
