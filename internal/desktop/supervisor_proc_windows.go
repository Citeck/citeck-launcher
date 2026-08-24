//go:build windows

package desktop

import (
	"os"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW from the Win32 process-creation flags.
// Hardcoded rather than pulled from golang.org/x/sys/windows so this file needs
// no import that the other platforms do not already carry.
const createNoWindow = 0x08000000

// sysProcAttrSetsid returns the process attributes for the supervised daemon on
// Windows. Setsid does not exist there; orphan reaping relies on the persisted
// daemon.pid (ReapOrphanDaemon) on next launch since there is no Pdeathsig
// equivalent.
//
// CREATE_NO_WINDOW is load-bearing and pairs with the wrapper's own -H
// windowsgui (packaging/windows/release.sh). The daemon is a console-subsystem
// binary, and CreateProcess gives such a child a BRAND NEW console — with a
// visible window — whenever the parent has none. So the moment the wrapper
// stopped being a console app, the console the user complained about would have
// come straight back, this time owned by the daemon. Redirecting the child's
// stdout/stderr (which the supervisor already does, into LogWriter) does not
// prevent the allocation: the window is created before anything is written to
// it.
//
// Grandchildren inherit the console-less state, so any console-subsystem
// process the DAEMON spawns would allocate a window of its own too. As of this
// change there are none: the daemon talks to Docker through the SDK, to git
// through go-git, and the only thing it execs on Windows is explorer.exe
// (routes_workspace.go), which is GUI-subsystem. Anything console-subsystem
// added there needs this flag as well.
func sysProcAttrSetsid() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNoWindow}
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
