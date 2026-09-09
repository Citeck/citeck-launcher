package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// detachStubDocker is the little Docker surface a namespace with no apps
// touches on Start / Detach: the network and the container inventory. Anything
// else panics on the embedded nil interface, which is the point — a test that
// grows past this shape should say so loudly.
type detachStubDocker struct {
	docker.RuntimeClient
}

func (detachStubDocker) ContainerName(app string) string { return "citeck_" + app }
func (detachStubDocker) CreateNetwork(context.Context) (string, error) {
	return "net", nil
}
func (detachStubDocker) RemoveNetwork(context.Context) error { return nil }
func (detachStubDocker) GetContainers(context.Context) ([]container.Summary, error) {
	return nil, nil
}
func (detachStubDocker) ListAllLauncherContainers(context.Context) ([]container.Summary, error) {
	return nil, nil
}
func (detachStubDocker) GetImageDigest(_ context.Context, img string) string {
	return "sha256:stub-" + img
}

var errDetachStoreRefused = errors.New("disk quota exceeded")

// refusingPersister refuses every write, like a full disk.
type refusingPersister struct{}

func (refusingPersister) SaveNamespaceState(_, _ string) error { return errDetachStoreRefused }

// acceptingPersister takes every write.
type acceptingPersister struct{}

func (acceptingPersister) SaveNamespaceState(_, _ string) error { return nil }

// newDetachTestDaemon stands up a Daemon whose active namespace carries a REAL,
// RUNNING namespace.Runtime over the given state store, so the shutdown route
// exercises the genuine detach — including its final state write.
func newDetachTestDaemon(t *testing.T, p namespace.NsStatePersister) (*http.ServeMux, *Daemon) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, detachStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetStatePersister(p)
	rt.Start(nil, false)
	// An app-less namespace never reaches RUNNING (checkStatus has nothing to
	// call running); STARTING is set by the loop applying cmdStart, so it is
	// the proof that the loop — the thing detach has to unwind — is alive.
	require.Eventually(t, func() bool { return rt.Status() != namespace.NsStatusStopped },
		10*time.Second, 20*time.Millisecond, "the runtime loop never came up")

	d := &Daemon{
		activeNs: &activeNamespace{runtime: rt, nsConfig: &namespace.Config{ID: "test"}},
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux, d
}

func postShutdown(t *testing.T, mux *http.ServeMux, query string) api.ActionResultDto {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, api.DaemonShutdown+query, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var dto api.ActionResultDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	return dto
}

// The detach window: `citeck install` (and the desktop quit) asks the daemon to
// exit WITHOUT stopping containers, and the very last thing that daemon does is
// write the state the next one will adopt them with. If that write is refused
// there is no loop left to retry it and the response, as written, said
// "Detaching daemon" and nothing else — so the operator swapped the binary on
// top of a state that had already been lost.
//
// The response now carries the verdict. That requires the runtime phases of the
// shutdown to run BEFORE the answer is written, which is the opposite of the
// ordinary shutdown route (respond, then tear down 100ms later).
func TestADetachShutdownReportsAStateWriteThatDidNotLand(t *testing.T) {
	mux, d := newDetachTestDaemon(t, refusingPersister{})

	res := postShutdown(t, mux, "?leave_running=true&wait_state=true")

	assert.True(t, res.Success, "the detach itself succeeded — the containers are running")
	assert.Contains(t, res.StateSaveError, errDetachStoreRefused.Error(),
		"the answer must name why the state the next daemon adopts is stale")
	assert.ErrorIs(t, d.activeNs.runtime.DetachStateError(), errDetachStoreRefused)
}

// The teardown's namespace half is one-shot, and the daemon may reach it from
// two places at once (the route and a SIGTERM). A second caller must get the
// recorded verdict, not a fresh nil — otherwise whichever path lost the race
// would report a clean detach over a lost state.
func TestASecondShutdownCallStillSeesTheDetachVerdict(t *testing.T) {
	_, d := newDetachTestDaemon(t, refusingPersister{})

	first := d.shutdownRuntime(true)
	require.ErrorIs(t, first, errDetachStoreRefused)
	require.ErrorIs(t, d.shutdownRuntime(true), errDetachStoreRefused,
		"the verdict must survive the one-shot guard")
	d.shutdownServer()
	d.shutdownServer() // idempotent
}

// The ordinary case must not cry wolf: a healthy box would otherwise have every
// upgrade refuse to proceed.
func TestACleanDetachShutdownReportsNoStateError(t *testing.T) {
	mux, _ := newDetachTestDaemon(t, acceptingPersister{})

	res := postShutdown(t, mux, "?leave_running=true&wait_state=true")

	assert.True(t, res.Success)
	assert.Empty(t, res.StateSaveError)
}

// The verdict is OPT-IN, and the default must stay fire-and-forget. The other
// caller of this route is the desktop wrapper on quit, whose POST is capped at
// 2s (daemonDialTimeout) because a quitting wrapper must not block on a slow
// daemon: a detach that always waited for the teardown would time that request
// out on any teardown longer than that and log "shutdown POST failed; will kill
// child" on an ordinary quit — without delivering the verdict either, since the
// response never arrives.
func TestADetachWithoutWaitStateStillAnswersImmediately(t *testing.T) {
	mux, d := newDetachTestDaemon(t, refusingPersister{})
	// A background goroutine that outlives the response window: the teardown's
	// phase 1 waits for those, so a route that ran it synchronously would be
	// measurably late here.
	const bgWork = 1500 * time.Millisecond
	d.bgWg.Go(func() { time.Sleep(bgWork) })

	start := time.Now()
	res := postShutdown(t, mux, "?leave_running=true")
	elapsed := time.Since(start)

	assert.True(t, res.Success)
	assert.Empty(t, res.StateSaveError, "no verdict was asked for, so none is reported")
	assert.Less(t, elapsed, bgWork/3, "and the answer must not wait for the teardown")
}

// A full shutdown stops the containers, so there is no state for a next daemon
// to adopt and nothing to report — and it must keep answering IMMEDIATELY,
// rather than inheriting the detach route's wait. Only the detach route pays
// that price, and only because its answer depends on the teardown's outcome.
//
// The daemon is given a background goroutine that outlives the response window
// on purpose: phase 1 of the teardown waits for those (up to 10s), so a route
// that ran the teardown synchronously would be measurably late here.
func TestAFullShutdownStillAnswersBeforeItTearsDown(t *testing.T) {
	mux, d := newDetachTestDaemon(t, refusingPersister{})
	const bgWork = 1500 * time.Millisecond
	d.bgWg.Go(func() { time.Sleep(bgWork) })

	start := time.Now()
	res := postShutdown(t, mux, "")
	elapsed := time.Since(start)

	assert.True(t, res.Success)
	assert.Empty(t, res.StateSaveError, "a stop is not a detach: nothing is left running to be stale about")
	assert.Less(t, elapsed, bgWork/3, "the full-shutdown route must not block on the teardown")
}
