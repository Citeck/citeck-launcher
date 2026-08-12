// Tests for suspending an app's liveness probe around a deliberate diagnostic.
//
// The motivating measurement: a `GC.heap_dump` on citeck_eproc stops the world
// for long enough to burn every probe inside it (a stop-the-world pause does not
// refuse the connection, it just never answers, so each probe spends its full
// timeout and counts as a failure). Even the widened ~60s window does not cover
// a multi-GB dump — so the launcher, which is the one asking for the dump, has
// to stop watching for exactly as long as it takes.
package namespace

import (
	"errors"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRunningProbedApp puts one RUNNING app with a due liveness probe into the
// runtime, so tickUnderLock would dispatch a probe on the very next call.
func seedRunningProbedApp(t *testing.T, r *Runtime, name string) {
	t.Helper()
	def := simpleApp(name, "postgres:17")
	def.LivenessProbe = &appdef.AppProbeDef{
		Exec:           &appdef.ExecProbeDef{Command: []string{"pg_isready"}},
		PeriodSeconds:  1,
		TimeoutSeconds: 1,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = NsStatusRunning
	r.apps[name] = &AppRuntime{
		Name:        name,
		Status:      AppStatusRunning,
		ContainerID: "cid-" + name,
		Def:         def,
	}
	r.livenessNextAt[name] = time.Now().Add(-time.Second)
}

func livenessPlanned(plans []dispatchPlan, appName string) bool {
	for _, p := range plans {
		if p.taskID.Op == workers.OpLivenessProbe && p.taskID.App == appName {
			return true
		}
	}
	return false
}

func TestSuspendLivenessProbe_NoDispatchWhileSuspendedAndResumesAfter(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")
	// A second app proves the suspension is per-app and not a global switch:
	// dumping one JVM must not blind the launcher to the rest of the namespace.
	seedRunningProbedApp(t, r, "emodel")

	release := r.SuspendLivenessProbe("eproc")

	plans := r.tickUnderLock()
	assert.False(t, livenessPlanned(plans, "eproc"), "suspended app must not be probed")
	assert.True(t, livenessPlanned(plans, "emodel"), "other apps stay watched")

	release()

	// The probe is due again only after a full period: the app has just been
	// through whatever the diagnostic did to it, so it gets a fresh window
	// rather than a probe that was already overdue when the dump started.
	plans = r.tickUnderLock()
	assert.False(t, livenessPlanned(plans, "eproc"), "resume must not fire an overdue probe immediately")

	r.mu.Lock()
	r.livenessNextAt["eproc"] = time.Now().Add(-time.Second)
	r.mu.Unlock()
	plans = r.tickUnderLock()
	assert.True(t, livenessPlanned(plans, "eproc"), "the probe must come back")
}

// A probe already in flight when the diagnostic starts will time out inside the
// pause and report a failure. Counting it would defeat the suspension.
func TestSuspendLivenessProbe_IgnoresResultsThatLandWhileSuspended(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")

	release := r.SuspendLivenessProbe("eproc")
	defer release()

	r.handleLivenessProbeResult(workers.Result{
		TaskID:  workers.TaskID{Op: workers.OpLivenessProbe, App: "eproc"},
		Payload: workers.LivenessProbePayload{Healthy: false},
	})

	r.mu.Lock()
	failures := r.livenessFailures["eproc"]
	r.mu.Unlock()
	assert.Zero(t, failures, "a probe result from the suspension window must be dropped")
}

// Failures accumulated BEFORE the diagnostic must not be carried over it
// either: the app spent the whole suspension unwatched, so what is known about
// it afterwards starts from zero.
func TestSuspendLivenessProbe_ClearsFailureCountWhileSuspended(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")

	r.mu.Lock()
	r.livenessFailures["eproc"] = 5
	r.mu.Unlock()

	release := r.SuspendLivenessProbe("eproc")
	defer release()
	r.tickUnderLock()

	r.mu.Lock()
	failures := r.livenessFailures["eproc"]
	r.mu.Unlock()
	assert.Zero(t, failures)
}

// Two diagnostics can overlap (a thread dump while a heap dump runs). The first
// release must not re-arm the probe under the second.
func TestSuspendLivenessProbe_IsRefcounted(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")

	releaseA := r.SuspendLivenessProbe("eproc")
	releaseB := r.SuspendLivenessProbe("eproc")

	releaseA()
	r.mu.Lock()
	r.livenessNextAt["eproc"] = time.Now().Add(-time.Second)
	r.mu.Unlock()
	assert.False(t, livenessPlanned(r.tickUnderLock(), "eproc"), "still suspended by the second holder")

	releaseB()
	r.mu.Lock()
	r.livenessNextAt["eproc"] = time.Now().Add(-time.Second)
	r.mu.Unlock()
	assert.True(t, livenessPlanned(r.tickUnderLock(), "eproc"))

	// A double release must not push the count negative — that would leave the
	// app permanently unwatched the next time anything suspends it.
	releaseB()
	releaseA()
	r.mu.Lock()
	count := r.livenessSuspended["eproc"]
	r.mu.Unlock()
	assert.Zero(t, count)
}

// The point of the helper: an operation that fails, panics or returns early
// still gives the app back its probe. An app left unwatched because a
// diagnostic errored is a worse failure than the one being diagnosed.
func TestWithLivenessSuspended_ReleasesOnTheErrorPath(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")

	wantErr := errors.New("attach client exit=127")
	err := r.WithLivenessSuspended("eproc", func() error {
		assert.False(t, livenessPlanned(r.tickUnderLock(), "eproc"), "suspended for the duration")
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	r.mu.Lock()
	count := r.livenessSuspended["eproc"]
	r.livenessNextAt["eproc"] = time.Now().Add(-time.Second)
	r.mu.Unlock()
	assert.Zero(t, count)
	assert.True(t, livenessPlanned(r.tickUnderLock(), "eproc"))
}

func TestWithLivenessSuspended_ReleasesOnPanic(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	defer r.Shutdown()
	seedRunningProbedApp(t, r, "eproc")

	assert.Panics(t, func() {
		_ = r.WithLivenessSuspended("eproc", func() error {
			panic("docker went away")
		})
	})

	r.mu.Lock()
	count := r.livenessSuspended["eproc"]
	r.mu.Unlock()
	assert.Zero(t, count)
}
