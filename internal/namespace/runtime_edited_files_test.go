package namespace

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntime_EditedFileOverlay_ReadsDiskContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "postgres"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "postgres", "postgresql.conf"), []byte("shared_buffers=256MB"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proxy", "lua"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "proxy", "lua", "auth.lua"), []byte("-- custom"), 0o644))

	md := newMockDocker()
	r := NewRuntime(testConfig(), md, dir)
	defer r.Shutdown()

	r.RestoreEditedFiles([]string{
		"postgres/postgresql.conf",
		"proxy/lua/auth.lua",
		"missing/file.txt", // file does not exist on disk — must be skipped silently
	})

	overlay := r.EditedFileOverlay(dir)
	require.Len(t, overlay, 2, "missing file must be skipped")
	require.Equal(t, []byte("shared_buffers=256MB"), overlay["postgres/postgresql.conf"])
	require.Equal(t, []byte("-- custom"), overlay["proxy/lua/auth.lua"])

	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"postgres/postgresql.conf", "proxy/lua/auth.lua"}, keys)
}

func TestRuntime_EditedFileOverlay_EmptyWhenNoEdits(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	overlay := r.EditedFileOverlay(t.TempDir())
	require.Nil(t, overlay, "expected nil overlay when editedFiles is empty")
}
