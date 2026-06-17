//go:build !windows

package daemon

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with the given pid exists. Signal 0
// probes existence without delivering a signal: ESRCH => gone; nil or EPERM
// (alive but owned by another user) => alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
