package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// A supervisor/installer observes the socket close before Start's last defer
// releases the profile lock. Allow that short shutdown tail, but never steal a
// lock or unlink a socket. A listening daemon/recovery is an immediate refusal.
func acquireDaemonProfileLock(home, socket string, wait time.Duration) (*os.File, error) {
	deadline := time.Now().Add(wait)
	for {
		lock, err := fsutil.AcquireProfileLock(home)
		if !errors.Is(err, fsutil.ErrProfileBusy) {
			if err != nil {
				return nil, fmt.Errorf("acquire daemon profile lock: %w", err)
			}
			return lock, nil
		}
		conn, dialErr := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("another daemon or offline recovery owns this profile: %w", err)
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("previous daemon has not released its profile lock: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
