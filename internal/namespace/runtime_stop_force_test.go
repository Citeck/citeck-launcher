package namespace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// stoppingAppForT23 puts one app into a STOPPING window that began at the
// fake clock's current time, with no stop timeout configured anywhere.
func stoppingAppForT23(t *testing.T) (*Runtime, *FakeClock, *AppRuntime) {
	t.Helper()
	fc := NewFakeClock(time.Unix(1_700_000_000, 0))
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir(), WithTestClock(fc))
	r.groupTimeout = time.Second
	r.defaultStopTimeout = 0
	app := &AppRuntime{Name: "rag", Def: simpleApp("rag", "rag:1")}
	r.mu.Lock()
	r.apps[app.Name] = app
	r.setAppStatus(app, AppStatusStopping)
	app.beginStopWindow(fc.Now())
	r.mu.Unlock()
	return r, fc, app
}

func forceRemovePlans(plans []dispatchPlan, appName string) int {
	n := 0
	for _, p := range plans {
		if p.taskID == (workers.TaskID{App: appName, Op: workers.OpStop}) {
			n++
		}
	}
	return n
}

// With nothing configured, the stop worker gives the container
// docker.DefaultStopTimeoutSec of SIGTERM. T23 must not act before that window
// (plus groupTimeout) is over — the field bug was a watchdog that assumed 10s.
func TestT23DefaultBudgetStartsFromTheWorkersStopWindow(t *testing.T) {
	r, fc, app := stoppingAppForT23(t)
	window := docker.DefaultStopTimeoutSec*time.Second + r.groupTimeout

	fc.Advance(window)
	assert.Zero(t, forceRemovePlans(r.tickUnderLock(), app.Name),
		"no forced remove while the worker's own stop window is still open")
	assert.False(t, app.stopForced)

	fc.Advance(time.Millisecond)
	assert.Equal(t, 1, forceRemovePlans(r.tickUnderLock(), app.Name),
		"past the window the stop is finished by force")
	assert.True(t, app.stopForced)
	assert.Equal(t, AppStatusStopping, app.Status, "the forced remove keeps the status")
}

// A status relabel of the stop in flight (StopApp promoting UPDATING, RestartApp
// promoting STOPPING, Shutdown) is not a new stop: the forced remove keeps its
// groupTimeout budget and a hung one still fails on time.
func TestARelabelDoesNotRestartTheForcedRemoveBudget(t *testing.T) {
	r, fc, app := stoppingAppForT23(t)
	fc.Advance(docker.DefaultStopTimeoutSec*time.Second + r.groupTimeout + time.Millisecond)
	require.Equal(t, 1, forceRemovePlans(r.tickUnderLock(), app.Name))

	r.mu.Lock()
	r.setAppStatus(app, AppStatusUpdating)
	r.mu.Unlock()

	fc.Advance(r.groupTimeout + time.Millisecond)
	r.tickUnderLock()
	assert.Equal(t, AppStatusStoppingFailed, app.Status,
		"a forced remove that outlives groupTimeout fails even after a relabel")
}

// The forced remove supersedes whatever stop was in flight — including
// makeStartingStopPlan, which also cleared "<app>-init". The init container
// is removed too, best-effort; the main container decides the result.
func TestTheForcedRemoveAlsoClearsTheInitContainer(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	res := r.makeForceRemovePlan("rag").fn(context.Background())
	require.NoError(t, res.Err)

	md.mu.Lock()
	defer md.mu.Unlock()
	assert.ElementsMatch(t, []string{"test-rag-init", "test-rag"}, md.removedContainerIDs)
}
