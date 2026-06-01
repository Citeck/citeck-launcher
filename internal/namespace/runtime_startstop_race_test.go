package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStopStartingAppRemovesMainContainer reproduces the start/stop race that
// leaked fixed-port containers (mailhog/onlyoffice/pgadmin) and stranded their
// host ports across namespaces.
//
// The window: an app is STARTING with its main container already created and
// started in Docker, but the start worker's Result carrying ContainerID has not
// yet been applied to runtime state (app.ContainerID == ""). A stop arriving in
// this window must still stop+remove the MAIN container — not only the (usually
// non-existent) "<app>-init" container, which would leave the main container
// running and holding its ports.
func TestStopStartingAppRemovesMainContainer(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	const appName = "mailhog"

	// Docker already has the running main container (the start worker created
	// it). There is no "<app>-init" container — mailhog has no init step.
	md.mu.Lock()
	md.containers[appName] = mockContainer{id: "container-1"}
	md.mu.Unlock()

	// Runtime sees the app as STARTING but has not yet recorded ContainerID.
	app := &AppRuntime{
		Name:        appName,
		Def:         simpleApp(appName, "mailhog/mailhog:v1.0.1"),
		Status:      AppStatusStarting,
		ContainerID: "",
	}
	r.mu.Lock()
	r.apps = map[string]*AppRuntime{appName: app}
	plans := r.beginGroupStopUnderLock([]*AppRuntime{app})
	r.mu.Unlock()

	for _, p := range plans {
		_ = p.fn(context.Background())
	}

	md.mu.Lock()
	_, leaked := md.containers[appName]
	md.mu.Unlock()
	require.False(t, leaked,
		"main container %q leaked: STARTING-phase stop must remove the main container, not only <app>-init", appName)
}
