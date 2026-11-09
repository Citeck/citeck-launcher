package fsutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfileLockExclusionAndRelease(t *testing.T) {
	home := t.TempDir()
	first, err := AcquireProfileLock(home)
	require.NoError(t, err)
	_, err = AcquireProfileLock(home)
	require.ErrorIs(t, err, ErrProfileBusy)
	require.NoError(t, first.Close())
	next, err := AcquireProfileLock(home)
	require.NoError(t, err)
	require.NoError(t, next.Close())
}
