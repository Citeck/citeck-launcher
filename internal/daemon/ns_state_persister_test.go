package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestNsStatePersisterRoundTrip(t *testing.T) {
	s, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer s.Close()

	p := nsStatePersister{store: s, wsID: "ws1", nsID: "nsA"}
	require.NoError(t, p.SaveNamespaceState("RUNNING", `{"status":"RUNNING","manualStoppedApps":["edi"]}`))

	st := loadNsStateFromStore(s, "ws1", "nsA")
	require.NotNil(t, st)
	require.Equal(t, namespace.NsStatusRunning, st.Status)
	require.Equal(t, []string{"edi"}, st.ManualStoppedApps)

	// missing namespace -> nil
	require.Nil(t, loadNsStateFromStore(s, "ws1", "missing"))
}
