//go:build linux

package desktop

import "syscall"

// daemonSysProcAttr returns the platform process attributes for the supervised
// daemon child. On Linux we set Pdeathsig so the kernel sends SIGTERM to the
// child if the wrapper (parent) dies unexpectedly — this is the primary orphan
// guard. Setsid detaches the child into its own session/process group so a
// terminal signal to the wrapper does not propagate to the daemon.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid:    true,
		Pdeathsig: syscall.SIGTERM,
	}
}
