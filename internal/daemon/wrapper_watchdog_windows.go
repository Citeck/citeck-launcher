//go:build windows

package daemon

import "os"

// processAlive reports whether a process with the given pid exists. Windows has
// no signal-0 probe; os.FindProcess opens a real handle and fails for a pid that
// no longer exists. Mirrors the wrapper's own isProcessAlive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
