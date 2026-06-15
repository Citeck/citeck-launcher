package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// TestResolveOpenDirPath covers the allowlist-driven open-dir path resolver,
// including the "snapshots" kind added so the Snapshots dialog's "Open NS Dir"
// opens the namespace snapshot folder (not the volumes base).
func TestResolveOpenDirPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	d := &Daemon{
		activeNs: &activeNamespace{
			workspaceID: "ws-test",
			nsConfig:    &namespace.Config{ID: "ns-test", Name: "Test NS"},
			volumesBase: filepath.Join(home, "vols"),
		},
	}

	t.Run("volumes returns the volumes base", func(t *testing.T) {
		got, err := d.resolveOpenDirPath("volumes")
		require.NoError(t, err)
		assert.Equal(t, d.activeNs.volumesBase, got)
	})

	t.Run("snapshots returns and creates the snapshots dir", func(t *testing.T) {
		want := filepath.Join(config.ResolveVolumesBase(d.activeNs.workspaceID, d.activeNs.nsConfig.ID), "snapshots")
		require.NoError(t, os.RemoveAll(want)) // ensure absent so we test creation

		got, err := d.resolveOpenDirPath("snapshots")
		require.NoError(t, err)
		assert.Equal(t, want, got)

		info, statErr := os.Stat(got)
		require.NoError(t, statErr, "snapshots dir must be created when absent")
		assert.True(t, info.IsDir())
	})

	t.Run("unknown kind is rejected", func(t *testing.T) {
		_, err := d.resolveOpenDirPath("bogus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})

	t.Run("volumes with no namespace configured errors", func(t *testing.T) {
		empty := &Daemon{}
		_, err := empty.resolveOpenDirPath("volumes")
		require.Error(t, err)
	})

	t.Run("snapshots with no namespace configured errors", func(t *testing.T) {
		empty := &Daemon{}
		_, err := empty.resolveOpenDirPath("snapshots")
		require.Error(t, err)
	})
}
