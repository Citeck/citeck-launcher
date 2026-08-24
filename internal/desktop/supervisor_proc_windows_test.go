//go:build windows

package desktop

import "testing"

// Once the wrapper is linked with -H windowsgui it has no console of its own,
// and CreateProcess answers that by allocating a fresh console — window and all
// — for any console-subsystem child. The daemon is exactly such a child, so
// without CREATE_NO_WINDOW the console window simply changes owner instead of
// going away. This only runs on a Windows host; the Linux CI job compiles the
// file (see the GOOS=windows step in `make check`) but cannot execute it.
func TestDaemonChildIsSpawnedWithoutAConsoleWindow(t *testing.T) {
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Fatal("daemonSysProcAttr returned nil")
	}
	if attr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("daemon child must be created with CREATE_NO_WINDOW (0x%08x), got flags 0x%08x",
			createNoWindow, attr.CreationFlags)
	}
}
