//go:build windows

package desktop

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	createMutexW  = kernel32.NewProc("CreateMutexW")
	releaseMutex  = kernel32.NewProc("ReleaseMutex")
	closeHandle   = kernel32.NewProc("CloseHandle")
)

const errorAlreadyExists = 183

type instanceLock struct {
	handle syscall.Handle
}

// AcquireInstanceLock ensures only one desktop process runs at a time using a Windows named mutex.
func AcquireInstanceLock() (*instanceLock, error) {
	name, _ := syscall.UTF16PtrFromString("Global\\CiteckLauncher")
	handle, _, err := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, fmt.Errorf("create mutex: %v", err)
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errorAlreadyExists {
		closeHandle.Call(handle)
		return nil, fmt.Errorf("another Citeck Desktop instance is already running")
	}
	return &instanceLock{handle: syscall.Handle(handle)}, nil
}

// Release releases the instance lock.
func (l *instanceLock) Release() {
	if l.handle != 0 {
		releaseMutex.Call(uintptr(l.handle))
		closeHandle.Call(uintptr(l.handle))
		l.handle = 0
	}
}

