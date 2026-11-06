package launchermcp

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootedSpringFilesAndSymlinkHome(t *testing.T) {
	base := t.TempDir()
	props := filepath.Join(base, "app", "emodel", "props")
	require.NoError(t, os.MkdirAll(props, 0o755))
	path := filepath.Join(props, "application-launcher.yml")
	require.NoError(t, os.WriteFile(path, []byte("hello: world\n"), 0o644))
	home := filepath.Join(t.TempDir(), "linked-home")
	if err := os.Symlink(base, home); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := PropsRoot(home, "emodel")
	require.NoError(t, err)
	defer root.Close()
	b, err := ReadYAML(root, "application-launcher.yml")
	require.NoError(t, err)
	require.Contains(t, string(b), "world")
	require.NoError(t, WriteYAML(root, "application-launcher.yml", []byte("hello: new\n")))
	for _, name := range []string{"../application.yml", "/application.yml", "application/x.yml", "application\\x.yml", "realm.json", "proxy.yml", "application.sh"} {
		_, err = ReadYAML(root, name)
		require.Error(t, err, name)
		require.Error(t, WriteYAML(root, name, []byte("x: y")), name)
	}
	require.Error(t, WriteYAML(root, "application-new.yml", []byte("x: y")), "never create a new file")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", MaxBytes+1)), 0o644))
	_, err = ReadYAML(root, "application-launcher.yml")
	require.ErrorContains(t, err, "exceeds")
	require.NoError(t, os.Remove(path))
	outside := filepath.Join(t.TempDir(), "secret.yml")
	require.NoError(t, os.WriteFile(outside, []byte("outside: secret"), 0o644))
	require.NoError(t, os.Symlink(outside, path))
	_, err = ReadYAML(root, "application-launcher.yml")
	require.Error(t, err)
	require.Error(t, WriteYAML(root, "application-launcher.yml", []byte("bad: write")))
	b, err = os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "outside: secret", string(b))
	require.NoError(t, os.Rename(props, props+"-old"))
	require.NoError(t, os.Symlink(filepath.Dir(outside), props))
	_, err = PropsRoot(base, "emodel")
	require.Error(t, err, "parent symlink must fail closed")
}
func TestRootedFileConcurrentLeafAndParentReplacement(t *testing.T) {
	base := t.TempDir()
	props := filepath.Join(base, "app", "app", "props")
	require.NoError(t, os.MkdirAll(props, 0o755))
	name := "application.yml"
	require.NoError(t, os.WriteFile(filepath.Join(props, name), []byte("inside: yes"), 0o644))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, name), []byte("outside: secret"), 0o644))
	root, err := PropsRoot(base, "app")
	require.NoError(t, err)
	defer root.Close()
	var done atomic.Bool
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for !done.Load() {
			_ = root.Remove(name)
			_ = root.Symlink(filepath.Join(outside, name), name)
			_ = root.Remove(name)
			_ = root.WriteFile(name, []byte("inside: yes"), 0o644)
		}
	}()
	for range 200 {
		b, e := ReadYAML(root, name)
		if e == nil {
			require.NotContains(t, string(b), "secret")
		}
		_ = WriteYAML(root, name, []byte("inside: written"))
	}
	done.Store(true)
	<-finished
	require.NoError(t, os.Rename(props, props+"-pinned"))
	require.NoError(t, os.Symlink(outside, props))
	// Existing root is the pinned directory, never its replacement.
	_ = WriteYAML(root, name, []byte("inside: pinned"))
	b, err := os.ReadFile(filepath.Join(outside, name))
	require.NoError(t, err)
	require.Equal(t, "outside: secret", string(b))
}
