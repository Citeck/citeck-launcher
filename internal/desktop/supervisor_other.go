//go:build !linux

package desktop

import "syscall"

// daemonSysProcAttr returns the platform process attributes for the supervised
// daemon child. Pdeathsig is Linux-only, so on macOS/Windows we only request a
// new session (Setsid) where supported. Orphan reaping on these platforms
// relies on ReapOrphanDaemon reading the persisted daemon.pid on next launch.
//
// Setsid is a unix-only field (absent on Windows), so the actual SysProcAttr is
// built by the build-tagged sysProcAttrSetsid helper.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return sysProcAttrSetsid()
}
