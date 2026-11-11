//go:build !windows

package daemon

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPFullDiscoveryExcludesFIFO(t *testing.T) {
	d := mcpFixture(t)
	fifo := filepath.Join(d.active().volumesBase, "app", "emodel", "props", "pipe.conf")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	require.Equal(t, []string{"./app/emodel/props/application-launcher.yml"}, mcpListedEditorPaths(t, d))
}
