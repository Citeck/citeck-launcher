package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSameFilesystemAnswersOnlyWhatItCanSee pins the whole contract: two paths
// under one mount are the same filesystem, two paths under different ones are
// not, and a path that is not there is an ERROR rather than a "different".
// The caller of this helper decides whether two writes have to fit in one
// place, so a guessed answer silently halves what it demands.
func TestSameFilesystemAnswersOnlyWhatItCanSee(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	same, err := SameFilesystem(dir, sub)
	require.NoError(t, err)
	assert.True(t, same, "a subdirectory with nothing mounted in between")

	_, err = SameFilesystem(dir, filepath.Join(dir, "not-there"))
	require.Error(t, err, "a missing path must not be answered as 'a different filesystem'")

	_, err = SameFilesystem(filepath.Join(dir, "not-there"), dir)
	require.Error(t, err, "either side missing is the same refusal")

	if runtime.GOOS == "linux" {
		// procfs is its own filesystem on every Linux, which makes it the one
		// pair a test can rely on being genuinely different.
		same, err := SameFilesystem(dir, "/proc")
		require.NoError(t, err)
		assert.False(t, same, "/proc is not the filesystem holding a temp dir")
	}
}
