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

// The record of the launcher's last S3 secret write reaches disk the moment
// it is set and survives unrelated writes, like the switch marker; setting it
// again unchanged writes nothing, and the runtime hands out copies, so a
// caller cannot change the record behind the persisted one.
func TestContentStorageSecretWriteRoundTripsThroughPersistState(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)

	w := &ContentStorageSecretWrite{Secret: "emodel/secret@seaweedfs-s3-credentials", KeyHash: "sha256:00:11",
		Modified: "2026-10-05T10:23:45.844528Z"}
	require.NoError(t, r.SetContentStorageSecretWrite(w))
	require.Equal(t, 1, fp.callCount(), "persisted inline")
	assert.Equal(t, w, decodeState(t, fp.lastJSON()).ContentStorageSecretWrite)

	w.Modified = "changed by the caller"
	assert.Equal(t, "2026-10-05T10:23:45.844528Z", r.ContentStorageSecretWrite().Modified, "the runtime keeps its own copy")
	r.ContentStorageSecretWrite().KeyHash = "changed through a getter"
	assert.Equal(t, "sha256:00:11", r.ContentStorageSecretWrite().KeyHash, "and hands out copies")

	r.mu.Lock()
	r.manualStoppedApps["edi"] = true
	_ = r.persistState()
	r.mu.Unlock()
	assert.NotNil(t, decodeState(t, fp.lastJSON()).ContentStorageSecretWrite, "an unrelated write keeps the record")

	require.NoError(t, r.SetContentStorageSecretWrite(r.ContentStorageSecretWrite()))
	assert.Equal(t, 2, fp.callCount(), "setting the same record again writes nothing")

	require.NoError(t, r.SetContentStorageSecretWrite(nil))
	assert.NotContains(t, fp.lastJSON(), "contentStorageSecretWrite", "a cleared record leaves no key behind")
}

// Restoring on the load path does not write; a refused write stays owed.
func TestContentStorageSecretWriteRestoreAndFailedWrite(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	w := &ContentStorageSecretWrite{Secret: "emodel/secret@seaweedfs-s3-credentials", KeyHash: "sha256:00:11"}
	r.RestoreContentStorageSecretWrite(w)
	assert.Zero(t, fp.callCount())
	assert.Equal(t, w, r.ContentStorageSecretWrite())

	r2 := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r2.SetStatePersister(failingPersister{})
	require.ErrorIs(t, r2.SetContentStorageSecretWrite(w), errPersistFailed)
	assert.True(t, r2.dirty.Load())
	assert.Equal(t, w, r2.ContentStorageSecretWrite(), "the in-memory record still serves this process")
}
