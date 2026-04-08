//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/citeck/citeck-launcher/internal/config"
)

// InstanceLock represents a file-based single-instance lock for the desktop process.
type InstanceLock struct {
	file *os.File
}

// AcquireInstanceLock ensures only one desktop process runs at a time.
// Returns a lock that must be released on exit.
func AcquireInstanceLock() (*InstanceLock, error) {
	pidPath := filepath.Join(config.RunDir(), "desktop.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o750); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}

	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // lock file only needs owner access
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: file descriptor fits in int
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another Citeck Desktop instance is already running")
	}

	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Sync()

	return &InstanceLock{file: f}, nil
}

// Release releases the instance lock.
func (l *InstanceLock) Release() {
	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN) //nolint:gosec // G115: file descriptor fits in int
		name := l.file.Name()
		_ = l.file.Close()
		_ = os.Remove(name)
	}
}

