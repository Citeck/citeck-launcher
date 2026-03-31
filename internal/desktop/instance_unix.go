//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/citeck/citeck-launcher/internal/config"
)

type instanceLock struct {
	file *os.File
}

// AcquireInstanceLock ensures only one desktop process runs at a time.
// Returns a lock that must be released on exit.
func AcquireInstanceLock() (*instanceLock, error) {
	pidPath := filepath.Join(config.RunDir(), "desktop.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}

	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // lock file only needs owner access
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("another Citeck Desktop instance is already running")
	}

	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Sync()

	return &instanceLock{file: f}, nil
}

// Release releases the instance lock.
func (l *instanceLock) Release() {
	if l.file != nil {
		syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		name := l.file.Name()
		l.file.Close()
		os.Remove(name)
	}
}

