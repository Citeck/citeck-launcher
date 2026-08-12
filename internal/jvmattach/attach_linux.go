//go:build linux

package jvmattach

import (
	"fmt"
	"syscall"
)

// Supported reports whether dynamic attach can work on this host at all.
// Linux-only: everywhere else the container's /proc is inside a VM the host
// cannot address.
const Supported = true

// sendQuit asks the JVM to start its attach listener. With the trigger file in
// place SIGQUIT does NOT print a thread dump to stdout — HotSpot's signal
// dispatcher checks for the file first.
func sendQuit(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGQUIT); err != nil {
		return fmt.Errorf("kill -QUIT %d: %w", pid, err)
	}
	return nil
}
