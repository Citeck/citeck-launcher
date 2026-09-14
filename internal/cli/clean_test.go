package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// `citeck clean` used to scan volume DIRECTORIES only, which is the server-mode
// layout. In desktop mode the data lives in Docker NAMED volumes, so a deleted
// namespace's PostgreSQL volume was invisible to the one command whose job is
// reclaiming disk — the only thing that removed it was the startup sweep, which
// did it unasked and has stopped.
func TestOrphanedNamedVolumesAreFoundForNamespacesTheStoreNoLongerHas(t *testing.T) {
	known := map[string]bool{"mine": true}
	vols := []docker.LauncherVolume{
		{Name: "citeck_volume_postgres2_mine_ws", Namespace: "mine", Workspace: "ws"},
		{Name: "citeck_volume_postgres2_gone_ws", Namespace: "gone", Workspace: "ws"},
		{Name: "citeck_volume_rabbitmq2_gone_ws", Namespace: "gone", Workspace: "ws"},
	}

	got := collectOrphanNamedVolumes(vols, known)

	require.Len(t, got, 2, "both volumes of the unknown namespace, and neither of the known one")
	assert.Equal(t, "citeck_volume_postgres2_gone_ws", got[0].Name)
	assert.Equal(t, "gone", got[0].Namespace)
	assert.Equal(t, "citeck_volume_rabbitmq2_gone_ws", got[1].Name)
}

// Same rule the containers scan follows: a volume with no namespace label
// cannot be attributed, so it is left alone rather than guessed at.
func TestAVolumeWithNoNamespaceLabelIsNeverAnOrphan(t *testing.T) {
	got := collectOrphanNamedVolumes([]docker.LauncherVolume{{Name: "stray"}}, map[string]bool{})
	assert.Empty(t, got)
}

// The keep set of `citeck clean` has the same hole the startup sweep had: a
// namespace row can outlive its workspace row, and a walk of workspaces →
// namespaces cannot see those. Here the consequence is the mirror image — clean
// would offer a live namespace's containers and DATA volumes for deletion, with
// the operator's confirmation obtained on a false premise.
func TestNamespacesWhoseWorkspaceRowIsGoneAreStillKnownToClean(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.SaveWorkspace(storage.WorkspaceDto{ID: "ykbsidq", Name: "live"}))
	require.NoError(t, store.SaveNamespaceConfig("ykbsidq", "5fn7t5q", "live ns", "id: 5fn7t5q\n"))
	// A namespace whose workspace row was never written (or was deleted before
	// the cascade existed) — exactly what 8 of 11 rows looked like on the
	// machine this was found on.
	require.NoError(t, store.SaveNamespaceConfig("default", "3q5h43y", "orphan ns", "id: 3q5h43y\n"))

	known, err := knownNamespaceIDsFromStore(store)
	require.NoError(t, err)

	assert.True(t, known["5fn7t5q"])
	assert.True(t, known["3q5h43y"], "a stored namespace is known even with no workspace row")
}
