package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestPersistNamespaceConfigValidates(t *testing.T) {
	s, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer s.Close()
	d := &Daemon{store: s, workspaceID: "ws1"}

	// invalid bytes are rejected and nothing is stored
	err = d.persistNamespaceConfig("ws1", "nsA", []byte("id: nsA\nproxy:\n  port: 70000\n"))
	require.Error(t, err)
	_, ok, _ := s.LoadNamespaceConfig("ws1", "nsA")
	require.False(t, ok, "invalid config must not be stored")

	// valid bytes are stored verbatim; name is denormalized from config
	raw := []byte("id: nsA\nname: Alpha\nproxy:\n  port: 80\n")
	require.NoError(t, d.persistNamespaceConfig("ws1", "nsA", raw))
	got, ok, _ := s.LoadNamespaceConfig("ws1", "nsA")
	require.True(t, ok)
	require.Equal(t, string(raw), got)
	list, _ := s.ListNamespaces("ws1")
	require.Len(t, list, 1)
	require.Equal(t, "Alpha", list[0].Name)

	// loader returns a parsed Config
	cfg, err := d.loadNamespaceConfigFromStore("ws1", "nsA")
	require.NoError(t, err)
	require.Equal(t, "Alpha", cfg.Name)

	// missing -> errNamespaceNotFound sentinel
	_, err = d.loadNamespaceConfigFromStore("ws1", "missing")
	require.ErrorIs(t, err, errNamespaceNotFound)
}
