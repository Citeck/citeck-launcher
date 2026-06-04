package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStoreNamespaceMapsToFiles(t *testing.T) {
	conf := t.TempDir()
	runtimeBase := filepath.Join(t.TempDir(), "runtime")
	s, err := NewFileStore(conf, runtimeBase)
	require.NoError(t, err)
	defer s.Close()

	// no file yet
	list, err := s.ListNamespaces("daemon")
	require.NoError(t, err)
	require.Empty(t, list)
	_, ok, err := s.LoadNamespaceConfig("daemon", "default")
	require.NoError(t, err)
	require.False(t, ok)

	// save config writes conf/namespace.yml verbatim
	cfg := "id: prod\nname: Production\nproxy:\n  port: 80\n"
	require.NoError(t, s.SaveNamespaceConfig("daemon", "prod", "Production", cfg))
	onDisk, err := os.ReadFile(filepath.Join(conf, "namespace.yml"))
	require.NoError(t, err)
	require.Equal(t, cfg, string(onDisk))

	// load returns the same bytes; list returns 0-or-1 with id+name
	got, ok, err := s.LoadNamespaceConfig("daemon", "prod")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, cfg, got)
	list, _ = s.ListNamespaces("daemon")
	require.Len(t, list, 1)
	require.Equal(t, "prod", list[0].ID)
	require.Equal(t, "Production", list[0].Name)

	// state maps to runtimeBase/{nsID}/state-{nsID}.json
	require.NoError(t, s.SaveNamespaceState("daemon", "prod", "RUNNING", `{"status":"RUNNING"}`))
	stateOnDisk, err := os.ReadFile(filepath.Join(runtimeBase, "prod", "state-prod.json"))
	require.NoError(t, err)
	require.Equal(t, `{"status":"RUNNING"}`, string(stateOnDisk)) //nolint:testifylint // verbatim byte storage, not semantic equality
	js, ok, err := s.LoadNamespaceState("daemon", "prod")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, `{"status":"RUNNING"}`, js) //nolint:testifylint // verbatim byte storage, not semantic equality
}
