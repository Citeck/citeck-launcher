package fsutil

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootedParentSurvivesReplacementAndRejectsSymlinks(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0o600))
	root, err := OpenCanonicalRoot(base)
	require.NoError(t, err)
	defer root.Close()
	parent, leaf, err := RootedParent(root, "a/b/file", true)
	require.NoError(t, err)
	defer parent.Close()
	require.NoError(t, AtomicWriteRooted(parent, leaf, []byte("inside"), 0o644))
	require.NoError(t, os.Rename(filepath.Join(base, "a", "b"), filepath.Join(base, "a", "old")))
	if err = os.Symlink(outside, filepath.Join(base, "a", "b")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	require.NoError(t, AtomicWriteRooted(parent, leaf, []byte("pinned"), 0o644))
	_, _, err = RootedParent(root, "a/b/file", false)
	require.Error(t, err)
	b, err := os.ReadFile(filepath.Join(outside, "file"))
	require.NoError(t, err)
	require.Equal(t, "outside", string(b))
	require.NoError(t, parent.Remove(leaf))
	require.NoError(t, parent.Symlink(filepath.Join(outside, "file"), leaf))
	_, err = OpenRegularRooted(parent, leaf)
	require.Error(t, err)
	for _, key := range []string{"../file", "/file", "a/../file", "a\\file", "C:file"} {
		_, _, err = RootedParent(root, key, true)
		require.Error(t, err, key)
	}
}
func TestRootedOpenConcurrentLeafSwapNeverReadsOutside(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("SECRET"), 0o600))
	root, err := OpenCanonicalRoot(base)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, AtomicWriteRooted(root, "file", []byte("inside"), 0o644))
	var done atomic.Bool
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for !done.Load() {
			_ = root.Remove("file")
			_ = root.Symlink(outside, "file")
			_ = root.Remove("file")
			_ = AtomicWriteRooted(root, "file", []byte("inside"), 0o644)
		}
	}()
	for range 300 {
		b, e := ReadRooted(root, "file", 1024)
		if e == nil {
			require.NotContains(t, string(b), "SECRET")
		}
	}
	done.Store(true)
	<-stopped
}
