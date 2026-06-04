//go:build windows

package cli

import "syscall"

// daemonSysProcAttr detaches the forked daemon from the parent console on
// Windows so it keeps running after the CLI exits. Setsid is Unix-only; the
// Windows equivalent is DETACHED_PROCESS (0x8) | CREATE_NEW_PROCESS_GROUP
// (0x200) via CreationFlags.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
