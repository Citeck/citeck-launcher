//go:build windows

package daemon

import "errors"

// diskSpace returns free and total disk space in GB for the given path.
func diskSpace(_ string) (freeGB, totalGB float64, err error) {
	return 0, 0, errors.New("disk space check not supported on Windows")
}
