package fsutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAvailableDiskSpaceReportsForARealDirAndZeroForAMissingOne pins the
// helper's whole contract: a real directory reports the bytes its filesystem
// has left, and anything unknown reports 0 rather than a guess. Callers gate
// on `avail > 0 && avail < needed`, so a path the OS cannot answer for must
// never look like "no space left".
func TestAvailableDiskSpaceReportsForARealDirAndZeroForAMissingOne(t *testing.T) {
	assert.Positive(t, AvailableDiskSpace(t.TempDir()))
	assert.Zero(t, AvailableDiskSpace("/definitely/not/a/path"))
}
