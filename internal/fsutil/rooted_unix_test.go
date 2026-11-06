//go:build !windows

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRootedSpecialFileDoesNotBlock(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, unix.Mkfifo(filepath.Join(base, "fifo"), 0o600))
	root, err := os.OpenRoot(base)
	require.NoError(t, err)
	defer root.Close()
	done := make(chan error, 1)
	go func() { _, e := OpenRegularRooted(root, "fifo"); done <- e }()
	select {
	case e := <-done:
		require.Error(t, e)
	case <-time.After(time.Second):
		t.Fatal("FIFO open blocked")
	}
}
