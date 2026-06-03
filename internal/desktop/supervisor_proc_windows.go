//go:build windows

package desktop

import (
	"os"
	"syscall"
)

// sysProcAttrSetsid returns empty process attributes on Windows. The Setsid
// field does not exist there; orphan reaping relies on the persisted daemon.pid
// (ReapOrphanDaemon) on next launch since there is no Pdeathsig equivalent.
func sysProcAttrSetsid() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// isProcessAlive reports whether a process with the given pid exists. On Windows
// there is no signal-0 probe; we open the process and signal-0 via os.Process,
// which returns an error if the process is gone.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal(0) is unsupported on Windows and always errors, so fall back to
	// Release and treat a successfully opened handle as alive. FindProcess on
	// Windows opens a real handle and fails for nonexistent pids.
	_ = p.Release()
	return true
}

func signalTerminate(pid int) { killWindowsProcess(pid) }
func signalKill(pid int)      { killWindowsProcess(pid) }

func killWindowsProcess(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
