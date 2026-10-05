package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pending content-storage switch reaches disk the moment it is set — the
// case it exists for is a daemon that dies before the default is switched —
// and every later unrelated write keeps it, since persistState rebuilds the
// record from Runtime fields.
func TestContentStorageSwitchRoundTripsThroughPersistState(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	require.NoError(t, r.SetContentStorageSwitch("content/storage@seaweedfs-s3"))
	require.Equal(t, 1, fp.callCount(), "persisted inline")
	assert.Equal(t, "content/storage@seaweedfs-s3", decodeState(t, fp.lastJSON()).ContentStorageSwitchPending)

	r.mu.Lock()
	r.manualStoppedApps["edi"] = true
	_ = r.persistState()
	r.mu.Unlock()
	assert.Equal(t, "content/storage@seaweedfs-s3", decodeState(t, fp.lastJSON()).ContentStorageSwitchPending,
		"an unrelated write keeps the marker")

	require.NoError(t, r.SetContentStorageSwitch("content/storage@seaweedfs-s3"))
	assert.Equal(t, 2, fp.callCount(), "setting the same value again writes nothing")

	require.NoError(t, r.SetContentStorageSwitch(""))
	assert.Empty(t, decodeState(t, fp.lastJSON()).ContentStorageSwitchPending)
	assert.NotContains(t, fp.lastJSON(), "contentStorageSwitchPending", "a cleared marker leaves no key behind")
}

// Restoring on the load path does not write (r.status is still the
// placeholder STOPPED there), and the value is read back.
func TestRestoreContentStorageSwitchDoesNotPersist(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	r.RestoreContentStorageSwitch("content/storage@seaweedfs-s3")
	assert.Zero(t, fp.callCount())
	assert.Equal(t, "content/storage@seaweedfs-s3", r.ContentStorageSwitch())
}

// A refused write is reported and stays owed: the loop tail retries it.
func TestSetContentStorageSwitchKeepsAFailedWriteOwed(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(failingPersister{})

	require.ErrorIs(t, r.SetContentStorageSwitch("content/storage@seaweedfs-s3"), errPersistFailed)
	assert.True(t, r.dirty.Load())
	assert.Equal(t, "content/storage@seaweedfs-s3", r.ContentStorageSwitch(), "the in-memory marker still serves this process")
}
