//go:build integration

package daemon

// Real-Docker proof that the startup orphan-sweep does not touch DATA:
// `make test-integration-sweep`.
//
// What a fake cannot prove: the sweep's removal phase goes through the moby
// client, and the difference between "removes the container" and "removes the
// container and its named volumes" is one bool in ContainerRemoveOptions plus
// the presence or absence of a VolumeRemove call. Both are invisible to a unit
// test, and the cost of getting them wrong is somebody's PostgreSQL data.
//
// The test creates a labeled orphan — a named volume with a file in it and a
// container mounting it — then runs the sweep's removal phase over that pair
// and asserts the container is gone while the volume, and the file in it, are
// still there. The network is created too (a launcher container joins it) and
// is asserted on for the same reason as the volume: the sweep must leave it.
//
// Everything goes through the production *docker.Client rather than a raw moby
// client: it is the code under test's own view of Docker, and it is what
// resolves the endpoint from the active docker CLI context (a rootless daemon
// publishes no /var/run/docker.sock).
//
// Safe to run beside real stands: every resource it creates is labeled with a
// namespace and workspace of its own, and it only ever asks the sweep to remove
// that one pair.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
)

const (
	sweepITNamespace = "zzsweepit"
	sweepITWorkspace = "zzsweepws"
	sweepITVolume    = "sweepitdata"
	sweepITMarker    = "data-that-must-survive"
)

func TestIntegration_OrphanSweepRemovesTheContainerAndKeepsTheData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dc, err := docker.NewClient(sweepITWorkspace, sweepITNamespace)
	require.NoError(t, err, "a real Docker daemon is required")
	t.Cleanup(func() { _ = dc.Close() })
	require.NoError(t, dc.Ping(ctx))
	require.NoError(t, dc.EnsureUtilsImage(ctx))

	sweepITCleanup(context.WithoutCancel(ctx), t, dc)
	t.Cleanup(func() { sweepITCleanup(context.WithoutCancel(ctx), t, dc) })

	_, err = dc.CreateNetwork(ctx)
	require.NoError(t, err)

	volumeName, err := dc.CreateVolume(ctx, sweepITVolume)
	require.NoError(t, err)

	// Put a file in the volume, so "the volume still exists" is not the whole
	// claim — the data in it has to be there too.
	out, code, err := dc.RunUtilsContainer(ctx,
		[]string{"sh", "-c", "echo " + sweepITMarker + " > /data/marker"},
		[]string{volumeName + ":/data"})
	require.NoError(t, err, "seed the volume: %s", out)
	require.Equal(t, 0, code, "seed the volume: %s", out)
	require.Equal(t, sweepITMarker, sweepITReadMarker(ctx, t, dc, volumeName))

	app := appdef.ApplicationDef{
		Name:    "sweepit",
		Image:   config.UtilsImage(),
		Cmd:     []string{"sleep", "3600"},
		Volumes: []string{volumeName + ":/data"},
		Kind:    appdef.KindThirdParty,
	}
	containerID, err := dc.CreateContainer(ctx, app, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, dc.StartContainer(ctx, containerID))

	removed := dc.RemoveOrphanContainers(ctx,
		[]docker.OrphanTarget{{NS: sweepITNamespace, WS: sweepITWorkspace}})
	assert.Equal(t, []string{sweepITNamespace}, removed)

	_, inspectErr := dc.InspectContainer(ctx, containerID)
	assert.Error(t, inspectErr, "the orphan container must be gone")

	vol, err := dc.GetVolumeByOriginalName(ctx, sweepITVolume)
	require.NoError(t, err)
	require.NotNil(t, vol, "the named volume must still exist")
	assert.Equal(t, sweepITMarker, sweepITReadMarker(ctx, t, dc, volumeName),
		"the data in it must be untouched")

	nets, err := dc.ListLauncherNetworks(ctx)
	require.NoError(t, err)
	found := false
	for _, n := range nets {
		if n.Name == dc.NetworkName() {
			found = true
		}
	}
	assert.True(t, found, "the network must still exist")
}

// sweepITReadMarker reads the marker back through a utils container, so the
// check works under rootless Docker where the host cannot read the volume dir.
func sweepITReadMarker(ctx context.Context, t *testing.T, dc *docker.Client, volumeName string) string {
	t.Helper()
	out, code, err := dc.RunUtilsContainer(ctx,
		[]string{"cat", "/data/marker"}, []string{volumeName + ":/data"})
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

func sweepITCleanup(ctx context.Context, t *testing.T, dc *docker.Client) {
	t.Helper()
	cs, err := dc.GetContainers(ctx)
	if err == nil {
		for _, c := range cs {
			if rmErr := dc.RemoveContainer(ctx, c.ID); rmErr != nil {
				t.Logf("cleanup: remove container %s: %v", c.ID, rmErr)
			}
		}
	}
	vol, err := dc.GetVolumeByOriginalName(ctx, sweepITVolume)
	if err == nil && vol != nil {
		if rmErr := dc.RemoveVolume(ctx, vol.Name); rmErr != nil {
			t.Logf("cleanup: remove volume %s: %v", vol.Name, rmErr)
		}
	}
	if rmErr := dc.RemoveNetwork(ctx); rmErr != nil {
		t.Logf("cleanup: remove network: %v", rmErr)
	}
}
