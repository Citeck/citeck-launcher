//go:build windows

package snapshot

import (
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// availableDiskSpace returns available bytes at the given path, or 0 if unknown.
// Uses the Win32 GetDiskFreeSpaceExW API (free bytes available to the caller).
func availableDiskSpace(path string) int64 {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var freeBytesAvailable uint64
	ret, _, _ := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		0, // lpTotalNumberOfBytes — unused
		0, // lpTotalNumberOfFreeBytes — unused
	)
	if ret == 0 {
		return 0
	}
	return int64(freeBytesAvailable) //nolint:gosec // free disk space won't exceed int64 max
}
