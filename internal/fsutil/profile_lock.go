package fsutil

import (
	"errors"
	"fmt"
	"os"
)

// ErrProfileBusy means another daemon or offline recovery owns this profile.
var ErrProfileBusy = errors.New("launcher profile is in use by a daemon or offline recovery")

// AcquireProfileLock serializes daemon lifetime and offline state repair. The
// file is never unlinked: replacing its inode would allow two lock owners.
// Advisory locking also releases ownership after a crash.
func AcquireProfileLock(home string) (*os.File, error) {
	root, err := OpenCanonicalRoot(home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := openProfileLock(root)
	if err != nil {
		return nil, fmt.Errorf("open profile lock: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("profile lock must be a regular file")
	}
	if err = lockProfileFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
