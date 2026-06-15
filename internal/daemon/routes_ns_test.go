package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestDeleteRemovesFromListEvenIfRtfilesRemain(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(func() { config.SetDesktopMode(false) })

	s, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	d := &Daemon{store: s, activeNs: &activeNamespace{workspaceID: "ws1"}}

	require.NoError(t, d.persistNamespaceConfig("ws1", "nsA", []byte("id: nsA\nname: Alpha\nproxy:\n  port: 80\n")))

	// Simulate a leftover rtfiles directory that the OLD directory scanner
	// would have surfaced as a ghost entry.
	require.NoError(t, os.MkdirAll(filepath.Join(config.NamespaceDir("ws1", "nsA"), "rtfiles"), 0o755))

	rows, err := s.ListNamespaces("ws1")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	require.NoError(t, s.DeleteNamespace("ws1", "nsA"))
	rows, err = s.ListNamespaces("ws1")
	require.NoError(t, err)
	require.Empty(t, rows, "deleted namespace must not appear even if rtfiles dir remains")
}
