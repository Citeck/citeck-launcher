//go:build !windows

package cli

import "syscall"

// daemonSysProcAttr detaches the forked daemon into its own session (setsid)
// so it survives the parent CLI process exiting.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
