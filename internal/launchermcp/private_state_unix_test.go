//go:build !windows

package launchermcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func TestPrivateStateRootedIORefusesSymlinksAndNeverWritesTheirTargets(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("outside-private-data"), 0o600))
	private := filepath.Join(base, "private")
	require.NoError(t, os.Mkdir(private, 0o700))
	path := filepath.Join(private, "state.json")
	require.NoError(t, os.Symlink(outside, path))
	_, err := OpenStore(path)
	require.Error(t, err)
	// Replacing a planted leaf uses a pinned parent and never follows the link.
	root, err := os.OpenRoot(private)
	require.NoError(t, err)
	require.NoError(t, fsutil.AtomicWriteRooted(root, "state.json", []byte("new-leaf"), 0o600))
	require.NoError(t, root.Close())
	b, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "outside-private-data", string(b))
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	parentAlias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(private, parentAlias))
	_, err = OpenStore(filepath.Join(parentAlias, "state.json"))
	require.Error(t, err)
}
