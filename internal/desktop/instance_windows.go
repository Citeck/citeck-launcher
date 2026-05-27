//go:build windows

package desktop

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"unsafe"

	"github.com/citeck/citeck-launcher/internal/config"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutexW = kernel32.NewProc("CreateMutexW")
	releaseMutex = kernel32.NewProc("ReleaseMutex")
	closeHandle  = kernel32.NewProc("CloseHandle")
)

const errorAlreadyExists = 183

type InstanceLock struct {
	handle syscall.Handle
}

// AcquireInstanceLock ensures only one desktop process runs at a time using a Windows named mutex.
func AcquireInstanceLock() (*InstanceLock, error) {
	name, _ := syscall.UTF16PtrFromString("Global\\CiteckLauncher")
	handle, _, err := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, fmt.Errorf("create mutex: %v", err)
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		closeHandle.Call(handle)
		// Kotlin parity: hand focus off to the running instance over the daemon
		// socket. Only swallow when the daemon actually answers — a stale mutex
		// without a live daemon must still surface as an error.
		notifyErr := NotifyExistingInstance(config.SocketPath())
		if notifyErr == nil {
			slog.Info("Another Citeck Desktop instance is running; raised its window and exiting")
			os.Exit(0)
		}
		slog.Warn("Mutex held but no live daemon to focus; treating as stale", "err", notifyErr)
		return nil, fmt.Errorf("another Citeck Desktop instance is already running")
	}
	return &InstanceLock{handle: syscall.Handle(handle)}, nil
}

// Release releases the instance lock.
func (l *InstanceLock) Release() {
	if l.handle != 0 {
		releaseMutex.Call(uintptr(l.handle))
		closeHandle.Call(uintptr(l.handle))
		l.handle = 0
	}
}
