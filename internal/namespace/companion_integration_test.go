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
// after the namespace reaches RUNNING, and a companion that was already up when
// the verdict arrived must still have one — the verdict decides what STARTS by
// itself and nothing else.
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

// The other half: the verdict never reaches a companion that is already UP.
// Stopping it is the operator's move — the launcher deciding to take down a
// store on its own is both a surprise and, with other services able to read
// that store later, wrong.
func TestIntegration_ARunningCompanionIsLeftAloneWhenItsOwnerDetaches(t *testing.T) {
	dc, r, apps := companionITSetup(t)

	r.Start(apps, false)
	require.True(t, waitForAppStatus(r, companionITCompanion, AppStatusRunning, 3*time.Minute))
	require.True(t, companionITHasContainer(t, dc, companionITCompanion))

	r.SetAutoDetachedApps(map[string]bool{companionITCompanion: true})

	assert.Equal(t, AppStatusRunning, r.FindApp(companionITCompanion).Status,
		"работающий companion остаётся работать")
	assert.True(t, companionITHasContainer(t, dc, companionITCompanion),
		"контейнер должен остаться на месте")
	assert.False(t, r.IsAppDetached(companionITCompanion),
		"и остаться под управлением цикла, а не числиться отцепленным")
	assert.NotContains(t, r.ManualStoppedApps(), companionITCompanion,
		"вердикт владельца не пишется в персистентный набор оператора")
}
