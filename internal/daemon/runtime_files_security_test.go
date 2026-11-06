package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

func TestRuntimeFilesRejectUnsafeEntriesAndPreserveOutside(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "secret")
	require.NoError(t, os.WriteFile(path, []byte("outside"), 0o600))
	if err := os.Symlink(outside, filepath.Join(base, "app")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.Error(t, writeRuntimeFiles(base, map[string][]byte{"app/secret": []byte("bad")}, nil))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "outside", string(b))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, os.Remove(filepath.Join(base, "app")))
	require.NoError(t, os.Symlink(path, filepath.Join(base, "file.sh")))
	require.Error(t, writeRuntimeFiles(base, map[string][]byte{"file.sh": []byte("outside")}, nil))
	info, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	for _, key := range []string{"../secret", path, "a/../../secret", "a\\secret", "a:secret"} {
		require.Error(t, writeRuntimeFiles(base, map[string][]byte{key: []byte("bad")}, nil), key)
	}
	linked := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.Symlink(base, linked))
	require.NoError(t, writeRuntimeFiles(linked, map[string][]byte{"safe/application.yml": []byte("a: b")}, nil))
}
func TestDiskOverlayUnavailableIsNotEmpty(t *testing.T) {
	base := t.TempDir()
	keys := []string{"empty.yml", "missing.yml", "big.yml", "link.yml"}
	edits := map[string]namespace.FileEdit{}
	for _, key := range keys {
		edits[key] = namespace.FileEdit{Format: "unknown"}
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "empty.yml"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "big.yml"), []byte(strings.Repeat("x", (1<<20)+1)), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(base, "empty.yml"), filepath.Join(base, "link.yml")))
	current := readDiskContent(base, edits)
	v, ok := current["empty.yml"]
	require.True(t, ok)
	require.NotNil(t, v)
	require.Empty(t, v)
	for _, key := range keys[1:] {
		_, ok = current[key]
		require.False(t, ok, key)
	}
}
func TestGenerateAndWritePropagatesMaterializationBeforeHandoff(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "app")))
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "test"
	// A guaranteed generated postgres directory is enough to drive the shared
	// generation path without any Docker/daemon. Its refusal must be returned.
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "postgres")))
	_, err := generateAndWriteRuntimeFiles(&cfg, &bundle.ResolveResult{Bundle: &bundle.Def{}, Workspace: &bundle.WorkspaceConfig{}}, namespace.SystemSecrets{}, namespace.GenerateOpts{}, base, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write runtime files")
}
