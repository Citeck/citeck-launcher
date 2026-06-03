//go:build !windows

package desktop

import "syscall"

// isProcessAlive reports whether a process with the given pid exists, using the
// signal-0 probe. Named distinctly from the test's processAlive to avoid a
// duplicate symbol in the package.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func signalTerminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }
func signalKill(pid int)      { _ = syscall.Kill(pid, syscall.SIGKILL) }
