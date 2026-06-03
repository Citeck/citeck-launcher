//go:build !windows

package desktop

import (
	"fmt"
	"log/slog"
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
		// Kotlin parity (core/utils/AppLock.kt + AppLocalSocket.kt): the second
		// launch hands focus off to the running instance instead of erroring.
		// Only swallow the lock conflict when the daemon actually answers —
		// a stale lock file with no live daemon must still surface as an error.
		notifyErr := NotifyExistingInstance(config.SocketPath())
		if notifyErr == nil {
			slog.Info("Another Citeck Desktop instance is running; raised its window and exiting")
			os.Exit(0)
		}
		slog.Warn("Lock held but no live daemon to focus; treating as stale", "err", notifyErr)
		return nil, fmt.Errorf("another Citeck Desktop instance is already running")
	}

	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Sync()

	// We are the primary instance. Reap a daemon left orphaned by a previously
	// crashed wrapper (macOS/Windows have no Pdeathsig, so a hard-killed wrapper
	// can leave its daemon child running). Best-effort — a failure here must not
	// block startup.
	if err := ReapOrphanDaemon(); err != nil {
		slog.Warn("Failed to reap orphan daemon on startup", "err", err)
	}

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
