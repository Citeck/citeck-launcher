package namespace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// addrDocker answers only the two calls AppHTTPAddress makes. Anything else
// hits the nil embedded interface and panics, which is the point: the helper
// must not touch Docker for anything but the address.
type addrDocker struct {
	docker.RuntimeClient
	published int
	ip        string
	askedPort int
}

func (d *addrDocker) GetPublishedPort(_ context.Context, _ string, containerPort int) int {
	d.askedPort = containerPort
	return d.published
}

func (d *addrDocker) GetContainerIP(context.Context, string) string { return d.ip }

func addrRuntime(dc docker.RuntimeClient, app *AppRuntime) *Runtime {
	r := &Runtime{docker: dc, apps: map[string]*AppRuntime{}, manualStoppedApps: map[string]bool{}}
	if app != nil {
		r.apps[app.Name] = app
	}
	return r
}

func gatewayApp() *AppRuntime {
	return &AppRuntime{
		Name: appdef.AppGateway, Status: AppStatusRunning, ContainerID: "c1",
		Def: appdef.ApplicationDef{
			Name:         appdef.AppGateway,
			Environments: appdef.OrderedMap{{Key: "SERVER_PORT", Value: "17020"}},
		},
	}
}

// Desktop: the port is published, and the daemon reaches it on loopback.
func TestAppHTTPAddress_PrefersThePublishedPort(t *testing.T) {
	dc := &addrDocker{published: 27020, ip: "172.18.0.5"}
	addr, err := addrRuntime(dc, gatewayApp()).AppHTTPAddress(context.Background(), appdef.AppGateway, 0)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:27020", addr)
	assert.Equal(t, 17020, dc.askedPort, "0 means the app's own SERVER_PORT")
}

// Server mode publishes nothing but the proxy: the container IP and the
// container port, as the startup probes do.
func TestAppHTTPAddress_FallsBackToTheContainerIP(t *testing.T) {
	dc := &addrDocker{published: -1, ip: "172.18.0.5"}
	addr, err := addrRuntime(dc, gatewayApp()).AppHTTPAddress(context.Background(), appdef.AppGateway, 0)
	require.NoError(t, err)
	assert.Equal(t, "172.18.0.5:17020", addr)

	addr, err = addrRuntime(dc, gatewayApp()).AppHTTPAddress(context.Background(), appdef.AppGateway, 8080)
	require.NoError(t, err)
	assert.Equal(t, "172.18.0.5:8080", addr, "an explicit port wins over SERVER_PORT")
}

func TestAppHTTPAddress_Refusals(t *testing.T) {
	ctx := context.Background()

	_, err := addrRuntime(&addrDocker{}, nil).AppHTTPAddress(ctx, appdef.AppGateway, 0)
	require.ErrorIs(t, err, ErrAppUnknown)

	r := addrRuntime(&addrDocker{}, nil)
	r.generatedDefs = map[string]appdef.ApplicationDef{appdef.AppGateway: {Name: appdef.AppGateway}}
	_, err = r.AppHTTPAddress(ctx, appdef.AppGateway, 0)
	require.ErrorIs(t, err, ErrAppNotRunning, "known but never started is not unknown")

	stopped := gatewayApp()
	stopped.Status = AppStatusStopped
	_, err = addrRuntime(&addrDocker{}, stopped).AppHTTPAddress(ctx, appdef.AppGateway, 0)
	require.ErrorIs(t, err, ErrAppNotRunning)

	noPort := gatewayApp()
	noPort.Def.Environments = nil
	_, err = addrRuntime(&addrDocker{}, noPort).AppHTTPAddress(ctx, appdef.AppGateway, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_PORT")

	_, err = addrRuntime(&addrDocker{published: -1}, gatewayApp()).AppHTTPAddress(ctx, appdef.AppGateway, 0)
	require.ErrorIs(t, err, ErrAppAddressUnknown)
}

// --- ReattachApp ---

func reattachRuntime(t *testing.T, status NsRuntimeStatus, app AppRuntimeStatus) (*Runtime, *fakePersister) {
	t.Helper()
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{"seaweedfs": {Name: "seaweedfs"}})
	r.status = status
	r.apps["seaweedfs"].Status = app
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	return r, fp
}

// On a live loop a re-attach must START the app: clearing the flag alone
// leaves it STOPPED for good — nothing steps a STOPPED app — with ecos-content
// parked in DEPS_WAITING behind it.
func TestReattachApp_OnALiveLoopStartsTheApp(t *testing.T) {
	r, fp := reattachRuntime(t, NsStatusRunning, AppStatusStopped)
	r.manualStoppedApps["seaweedfs"] = true

	assert.True(t, r.ReattachApp("seaweedfs"))
	assert.False(t, r.IsAppDetached("seaweedfs"))
	assert.Equal(t, AppStatusReadyToPull, r.apps["seaweedfs"].Status)
	require.Equal(t, 1, fp.callCount(), "the cleared detach is persisted at once")
	assert.NotContains(t, decodeState(t, fp.lastJSON()).ManualStoppedApps, "seaweedfs")
}

// On a stopped namespace there is nothing to step: the persisted detach is
// cleared, and the app starts with the namespace.
func TestReattachApp_OnAStoppedNamespaceClearsTheDetachOnly(t *testing.T) {
	r, fp := reattachRuntime(t, NsStatusStopped, AppStatusStopped)
	r.manualStoppedApps["seaweedfs"] = true

	assert.True(t, r.ReattachApp("seaweedfs"))
	assert.False(t, r.IsAppDetached("seaweedfs"))
	assert.Equal(t, AppStatusStopped, r.apps["seaweedfs"].Status, "nothing runs to step it")
	assert.Equal(t, 1, fp.callCount())
}

// A namespace never started in this process has an empty app table; the
// detach still lives in manualStoppedApps and is still cleared.
func TestReattachApp_WithNoAppTableEntry(t *testing.T) {
	r, fp := reattachRuntime(t, NsStatusStopped, AppStatusStopped)
	delete(r.apps, "seaweedfs")
	r.manualStoppedApps["seaweedfs"] = true

	assert.True(t, r.ReattachApp("seaweedfs"))
	assert.False(t, r.IsAppDetached("seaweedfs"))
	assert.Equal(t, 1, fp.callCount())
}

// Nothing to do is nothing done — not even a write.
func TestReattachApp_LeavesARunningAppAlone(t *testing.T) {
	r, fp := reattachRuntime(t, NsStatusRunning, AppStatusRunning)
	assert.False(t, r.ReattachApp("seaweedfs"))
	assert.Equal(t, AppStatusRunning, r.apps["seaweedfs"].Status)
	assert.Zero(t, fp.callCount())
}
