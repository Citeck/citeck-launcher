//go:build integration

package namespace

// Real-Docker proof of the ONE promise the companion rule makes:
// `make test-integration-companion`.
//
// A companion (qdrant beside rag, stt-sidecar beside ai) is now GENERATED while
// its owner is detached — that is what lets somebody stop the owner here and run
// it from an IDE against this stand. Everything then rests on the runtime never
// starting it by itself, and a unit test with a fake Docker cannot prove that:
// the fake records the calls the runtime chose to make, and what matters is
// whether a container exists on the host at the end.
//
// So both cases below ask Docker: an auto-detached app must have NO container
// after the namespace reaches RUNNING, and a companion that was up when the
// verdict arrived must have its container GONE, without the operator's detach
// set ever mentioning it (that map is persisted and would outlive the owner).
//
// Safe to run beside real stands: its own namespace and workspace labels, the
// utils image, no published ports, no named volumes.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
)

const (
	companionITNamespace = "zzcompanionit"
	companionITWorkspace = "zzcompanionws"
	companionITOwner     = "owner"
	companionITCompanion = "companion"
)

func companionITSetup(t *testing.T) (*docker.Client, *Runtime, []appdef.ApplicationDef) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	dc, err := docker.NewClient(companionITWorkspace, companionITNamespace)
	require.NoError(t, err, "a real Docker daemon is required")
	require.NoError(t, dc.Ping(ctx))
	require.NoError(t, dc.EnsureUtilsImage(ctx))

	companionITCleanup(dc)
	t.Cleanup(func() { companionITCleanup(dc) })

	cfg := &Config{ID: companionITNamespace, Name: "Companion IT"}
	r := NewRuntime(cfg, dc, t.TempDir())
	t.Cleanup(func() {
		r.Stop()
		r.Shutdown()
	})

	// Long-running, cheap, and with no startup probe: what is under test is the
	// runtime's decision to start a container at all, not what runs in it.
	app := func(name string) appdef.ApplicationDef {
		return appdef.ApplicationDef{
			Name:  name,
			Image: config.UtilsImage(),
			Cmd:   []string{"sleep", "3600"},
			Kind:  appdef.KindThirdParty,
		}
	}
	return dc, r, []appdef.ApplicationDef{app(companionITOwner), app(companionITCompanion)}
}

func companionITCleanup(dc *docker.Client) {
	dc.RemoveOrphanContainers(context.Background(),
		[]docker.OrphanTarget{{NS: companionITNamespace, WS: companionITWorkspace}})
}

func companionITHasContainer(t *testing.T, dc *docker.Client, appName string) bool {
	t.Helper()
	_, err := dc.InspectContainer(context.Background(), dc.ContainerName(appName))
	return err == nil
}

// An app the generator marked auto-detached must not reach Docker at all, while
// everything beside it starts normally — and an explicit start must still work,
// because that is the debugging case the spec is kept in the namespace for.
func TestIntegration_AutoDetachedCompanionNeverReachesDocker(t *testing.T) {
	dc, r, apps := companionITSetup(t)

	r.SetAutoDetachedApps(map[string]bool{companionITCompanion: true})
	r.Start(apps, false)

	require.True(t, waitForAppStatus(r, companionITOwner, AppStatusRunning, 3*time.Minute),
		"соседнее приложение должно подняться как обычно")
	assert.False(t, companionITHasContainer(t, dc, companionITCompanion),
		"auto-detached приложение не должно создать контейнер")

	require.NoError(t, r.StartApp(companionITCompanion))
	require.True(t, waitForAppStatus(r, companionITCompanion, AppStatusRunning, 3*time.Minute),
		"явный старт должен перебить auto-detach")
	assert.True(t, companionITHasContainer(t, dc, companionITCompanion))
}

// The other half: a companion that is UP when its owner is detached goes down
// for real. A container left running under a STOPPED row is a ghost — and the
// memory it holds is exactly what "a switched-off owner costs nothing" promises.
func TestIntegration_CompanionContainerGoesDownWhenTheOwnerDetaches(t *testing.T) {
	dc, r, apps := companionITSetup(t)

	r.Start(apps, false)
	require.True(t, waitForAppStatus(r, companionITCompanion, AppStatusRunning, 3*time.Minute))
	require.True(t, companionITHasContainer(t, dc, companionITCompanion))

	r.SetAutoDetachedApps(map[string]bool{companionITCompanion: true})

	require.True(t, waitForAppStatus(r, companionITCompanion, AppStatusStopped, 2*time.Minute),
		"companion должен остановиться сам")
	assert.False(t, companionITHasContainer(t, dc, companionITCompanion),
		"контейнер должен быть снят, а не оставлен жить под статусом STOPPED")
	assert.Equal(t, AppStatusRunning, r.FindApp(companionITOwner).Status,
		"соседа это трогать не должно")
	assert.NotContains(t, r.ManualStoppedApps(), companionITCompanion,
		"это состояние владельца, а не намерение оператора: manualStoppedApps персистится")
}
