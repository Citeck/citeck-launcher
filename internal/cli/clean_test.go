package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
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

// A SERVER install owns no launcher-labeled named volume at all — it binds
// every plain volume source into its runtime directory, and only the desktop
// path calls CreateVolume, which is what applies the labels. Its keep set is
// also a single namespace id read out of namespace.yml. So a server-mode scan
// of named volumes could only ever list somebody else's desktop stand, and then
// offer its PostgreSQL data for deletion under a confirmation the operator gave
// about their own orphans.
//
// The client is deliberately nil: the gate has to come BEFORE Docker is asked,
// so moving it below the listing makes this test panic rather than pass.
func TestServerModeOffersNoNamedVolumeForDeletion(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	got, err := findOrphanNamedVolumes(context.Background(), nil, map[string]bool{"the-one-server-ns": true})

	require.NoError(t, err)
	assert.Empty(t, got, "a server install never owns a named volume, so it may never offer one")
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

// A developer machine routinely runs BOTH a desktop profile and a server
// install, and neither one's keep set can contain the other's namespaces — so
// each read the other's live stand as orphaned and offered it for deletion. The
// workspace label is what separates them: `Client.workspace` is the workspace id
// in desktop mode and the empty string in server mode. Same rule the startup
// sweep learned (collectOrphanTargets skips an empty workspace), except here it
// guards a confirmation the operator is about to give.
func TestOnlyThisInstallationsResourcesAreOffered(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	if !belongsToThisProfile("some-ws") {
		t.Errorf("desktop: a workspace-labeled resource is this profile's")
	}
	if belongsToThisProfile("") {
		t.Errorf("desktop: an unlabeled workspace means a SERVER install's live stand")
	}

	config.SetDesktopMode(false)
	if !belongsToThisProfile("") {
		t.Errorf("server: server mode writes an empty workspace, so that is ours")
	}
	if belongsToThisProfile("some-ws") {
		t.Errorf("server: a workspace-labeled resource belongs to a desktop profile")
	}

}
