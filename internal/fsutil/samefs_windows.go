//go:build windows

package fsutil

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var getVolumePathNameW = kernel32.NewProc("GetVolumePathNameW")

// SameFilesystem reports whether two paths live on the same filesystem.
//
// Windows has no st_dev, so the identity is the VOLUME MOUNT POINT the path
// resolves to — "C:\" for an ordinary path, or the directory a volume is
// mounted on when one is mounted into another's tree. That is the same
// distinction st_dev draws on unix: two paths with one mount root share the
// free space that a caller is about to spend twice.
//
// A path the API cannot resolve is an ERROR, never an answer, for the reason
// stated on the unix implementation: a guessed "different" halves what the
// caller demands.
func SameFilesystem(a, b string) (bool, error) {
	va, err := volumePathName(a)
	if err != nil {
		return false, err
	}
	vb, err := volumePathName(b)
	if err != nil {
		return false, err
	}
	// Windows paths are case-insensitive, and the API is free to answer with
	// either spelling of the mount root.
	return strings.EqualFold(va, vb), nil
}

// volumePathName is the mount point of the volume holding path (Win32
// GetVolumePathNameW), e.g. `C:\`.
func volumePathName(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", fmt.Errorf("volume of %s: %w", path, err)
	}
	buf := make([]uint16, syscall.MAX_PATH+1)
	ret, _, callErr := getVolumePathNameW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if ret == 0 {
		return "", fmt.Errorf("volume of %s: %w", path, callErr)
	}
	return syscall.UTF16ToString(buf), nil
}
