// Behavioral tests for cmdRegenerate per-app diff + cmdDetach CancelAll +
// T32 GC.
//
// doRegenerate computes a per-app diff against the current r.apps map.
// Unchanged-hash apps stay RUNNING; changed-hash apps enter STOPPING +
// desiredNext=READY_TO_PULL + initialSweep=true (long-stop budget); removed
// apps enter markedForRemoval=true + STOPPING / STOPPED. T32 GCs STOPPED +
// markedForRemoval entries from r.apps.
//
// doDetach CancelAlls with reason=Detach and waits on the dispatcher's
// workerWg. Canceled Results that arrive with res.Err != nil are dropped
// silently by applyWorkerResult so a mid-flight pull doesn't
// mutate state into PULL_FAILED during detach.
package namespace

import (
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCmdRegenerateChangedHashUsesLongTimeout pins the contract that a
// reload-triggered recreate uses longStopTimeout (not groupTimeout). Java
// webapps routinely take 30–45s to honor SIGTERM; a 10s groupTimeout would
// false-positive them as STOPPING_FAILED and discard desiredNext.
//
// Setup: postgres RUNNING → Regenerate with new image → hash mismatch →
// doRegenerate sets STOPPING + initialSweep=true + desiredNext=READY_TO_PULL.
// stopDelay=3s; groupTimeout=1s; longStopTimeout=5s. tick() T23 must pick
// longBudget so the stop completes cleanly and the app walks back to RUNNING.
func TestCmdRegenerateChangedHashUsesLongTimeout(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	// Compress budgets to keep the test fast while preserving the contrast.
	r.groupTimeout = 1 * time.Second
	r.longStopTimeout = 5 * time.Second
	r.tickerPeriod = 50 * time.Millisecond
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	// Inject the 3s stopDelay AFTER startup so initial pulls/creates aren't
	// affected (simpleApp defaults to KindThirdParty with no pull needed).
	md.mu.Lock()
	md.stopDelay = 3 * time.Second
	md.mu.Unlock()

	// Regenerate with a changed image → hash mismatch → doRegenerate recreate.
	apps2 := []appdef.ApplicationDef{simpleApp("postgres", "postgres:18")}
	r.Regenerate(apps2, nil, nil)

	// The app must enter STOPPING with initialSweep=true.
	var sawInitialSweep bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		app, ok := r.apps["postgres"]
		if ok && app.Status == AppStatusStopping && app.initialSweep {
			sawInitialSweep = true
			r.mu.RUnlock()
			break
		}
		r.mu.RUnlock()
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, sawInitialSweep,
		"changed-hash app did not enter STOPPING with initialSweep=true")

	// Sample at ~1.5s: past 1s groupTimeout, well before 3s stopDelay.
	// App must still be STOPPING — T23 must NOT have fired.
	time.Sleep(1500 * time.Millisecond)
	app := r.FindApp("postgres")
	require.NotNil(t, app)
	assert.Equal(t, AppStatusStopping, app.Status,
		"long-stop budget not honored on reload-triggered recreate: app transitioned out of STOPPING before 3s stopDelay elapsed")

	// Allow the stop to complete + state machine to walk RUNNING.
	if !waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second) {
		a := r.FindApp("postgres")
		status := "nil"
		if a != nil {
			status = string(a.Status)
		}
		t.Fatalf("app did not reach RUNNING after reload-triggered recreate, got %s", status)
	}

	// Clear the delay so the deferred Shutdown() doesn't hang.
	md.mu.Lock()
	md.stopDelay = 0
	md.mu.Unlock()
}

// TestCmdRegenerateDeletesRemovedApp pins T32: an app removed from the desired
// set is marked for removal, driven to STOPPED via the state machine, and then
// GC'd from r.apps by stepAllApps. Contrast: a STOPPED app with
// markedForRemoval=false is NOT deleted.
func TestCmdRegenerateDeletesRemovedApp(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 50 * time.Millisecond
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
		simpleApp("mongo", "mongo:4"),
	}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	// Regenerate with postgres only — mongo must be dropped.
	r.Regenerate([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}, nil, nil)

	// Mongo must eventually vanish from r.apps (T32 GC).
	deadline := time.Now().Add(10 * time.Second)
	var gone bool
	for time.Now().Before(deadline) {
		r.mu.RLock()
		_, exists := r.apps["mongo"]
		r.mu.RUnlock()
		if !exists {
			gone = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, gone, "T32 GC did not delete removed-from-desired-set app from r.apps")

	// Postgres must still be RUNNING (unchanged-hash path is a no-op in
	// doRegenerate).
	app := r.FindApp("postgres")
	require.NotNil(t, app)
	assert.Equal(t, AppStatusRunning, app.Status,
		"unchanged-hash app must stay RUNNING across Regenerate (no-op path)")
}

// TestDetachDoesNotMutateStatusDuringPull pins the cancel-drop rule: an
// in-flight worker that errors after being canceled with CancelDetach is
// dropped silently by applyWorkerResult. Setup: force an app into PULLING with
// mockDocker.pullBlock; call ShutdownDetached(); the pull's ctx.Err()-flavored
// Result must NOT mutate app state (it would normally route to PULL_FAILED
// via T6).
//
// Because ShutdownDetached() waits for the runtime loop to exit before
// returning, we observe the final state after detach rather than during —
// the key invariant is that r.apps["emodel"].Status remained PULLING and
// was NOT overwritten to PULL_FAILED.
func TestDetachDoesNotMutateStatusDuringPull(t *testing.T) {
	md := newMockDocker()
	// Force a real pull: imageExists=false → T2 dispatches pull worker.
	// Set both fields under md.mu so the worker goroutine sees them with
	// proper happens-before ordering (PullImageWithProgress reads pullBlock
	// under md.mu.Lock()).
	md.mu.Lock()
	md.imageExists = map[string]bool{"ecos-model:2.0": false}
	md.pullBlock = make(chan struct{})
	md.mu.Unlock()

	r := NewRuntime(testConfig(), md, t.TempDir())

	var once sync.Once
	unblock := func() {
		once.Do(func() {
			md.mu.Lock()
			ch := md.pullBlock
			md.mu.Unlock()
			close(ch)
		})
	}
	defer unblock()

	def := simpleApp("emodel", "ecos-model:2.0")
	r.Start([]appdef.ApplicationDef{def})

	// Wait until the app enters PULLING (pull worker has been dispatched
	// and is now blocked on md.pullBlock).
	require.True(t, waitForAppStatus(r, def.Name, AppStatusPulling, 5*time.Second),
		"app did not reach PULLING for detach race setup")
	// Defensive stabilization: give the pull worker a moment to fully enter
	// the blocking select on md.pullBlock. waitForAppStatus only observes the
	// app status transition — the worker may still be between pullCalls++ and
	// the select. ShutdownDetached cancels via CancelDetach; if the worker
	// has not yet entered the select, it may race on ctx observation.
	time.Sleep(20 * time.Millisecond)

	// ShutdownDetached triggers doDetach → CancelAll(CancelDetach). The pull
	// worker's ctx is canceled; it observes ctx.Done() in its own select (see
	// mockDocker.PullImageWithProgress) and returns a ctx.Err Result.
	// applyWorkerResult drops it silently (CancelDetach rule) so PULLING state is preserved
	// (no T6 transition to PULL_FAILED).
	r.ShutdownDetached()

	// Post-detach: runtime loop exited. The app's last observable status
	// must still be PULLING (never transitioned to PULL_FAILED via T6).
	app := r.FindApp(def.Name)
	require.NotNil(t, app, "app must still be tracked after detach (no GC on detach)")
	assert.Equal(t, AppStatusPulling, app.Status,
		"CancelDetach drop-rule violated: PULLING app transitioned to %s", app.Status)
}
