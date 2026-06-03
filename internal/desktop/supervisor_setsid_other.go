//go:build !linux && !windows

package desktop

import "syscall"

// sysProcAttrSetsid requests a new session for the child on non-Linux unix
// platforms (darwin, freebsd, etc.) where the Setsid field exists. Used by the
// !linux daemonSysProcAttr in supervisor_other.go.
func sysProcAttrSetsid() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
