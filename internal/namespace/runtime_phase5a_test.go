// Behavioral tests for the state-machine-driven stale-container sweep.
// doStart routes existing+mismatched-hash containers through STOPPING +
// desiredNext=READY_TO_PULL + initialSweep=true. tick() T23 honors the
// longer `longStopTimeout` budget when initialSweep is set so Java SIGTERM
// handlers (30–45s) don't trip the 10s groupTimeout.
package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitialSweepUsesLongerTimeout exercises the stale-container sweep
// contract: when doStart encounters an existing container with a stale hash,
// it enters the app as STOPPING with initialSweep=true. tick() T23 must then
// use `longStopTimeout` (5s in this test) rather than `groupTimeout` (1s) to
// gauge the STOPPING budget. A stop that takes 3s — longer than the
// groupTimeout but within the longStopTimeout — must therefore complete
// cleanly (T21 → READY_TO_PULL → … → RUNNING), not fail into STOPPING_FAILED.
//
// Contrast: an operator-initiated stop (StopApp) has initialSweep=false and
// is bound by the 1s groupTimeout — the same 3s stopDelay would trip T23 and
// route to STOPPING_FAILED. The second half of the test asserts this inverse.
func TestInitialSweepUsesLongerTimeout(t *testing.T) {
	md := newMockDocker()
	// 3s stop delay: longer than groupTimeout (1s) but shorter than
	// longStopTimeout (5s). Initial-sweep STOPPING MUST ride the 5s budget
	// and complete; operator-initiated STOPPING (T19) MUST ride the 1s budget
	// and fail.
	md.stopDelay = 3 * time.Second

	// Seed a stale container: same app name, MISMATCHED hash. doStart
	// sees existing+!reuse+!detached → STOPPING + initialSweep=true +
	// desiredNext=READY_TO_PULL.
	def := simpleApp("postgres", "postgres:17")
	md.mu.Lock()
	md.nextID++
	md.containers[def.Name] = mockContainer{
		id: "container-stale",
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: "stale-hash-does-not-match",
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	r := NewRuntime(testConfig(), md, t.TempDir())
	// Compress budgets to keep the test fast while preserving behavioral
	// contrast. Production defaults (groupTimeout=10s, longStopTimeout=60s,
	// Docker default 10s) would mask the difference between the two paths.
	r.groupTimeout = 1 * time.Second
	r.longStopTimeout = 5 * time.Second
	r.defaultStopTimeout = 1 // 1s so per-app budget = 1s + groupTimeout = 2s
	defer r.Shutdown()

	// Baseline: no stops or pulls recorded yet (the seeded container was
	// inserted directly into the mock map).
	md.mu.Lock()
	pullsBefore := md.pullCalls
	stopsBefore := md.stopRemoveCalls
	md.mu.Unlock()

	r.Start([]appdef.ApplicationDef{def})

	// The app must enter STOPPING with initialSweep=true. Sample quickly
	// before the 3s stopDelay elapses.
	var sawInitialSweep bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		app, ok := r.apps[def.Name]
		if ok && app.Status == AppStatusStopping && app.initialSweep {
			sawInitialSweep = true
			r.mu.RUnlock()
			break
		}
		r.mu.RUnlock()
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, sawInitialSweep,
		"app did not enter STOPPING with initialSweep=true after Start")

	// Confirm the 1s groupTimeout would have fired here if the tick
	// predicate used groupBudget. We sample the state at ~1.5s (past 1s
	// groupTimeout, well before 3s stopDelay). The app MUST still be
	// STOPPING — not STOPPING_FAILED.
	time.Sleep(1500 * time.Millisecond)
	r.mu.RLock()
	mid := r.apps[def.Name]
	midStatus := AppRuntimeStatus("<missing>")
	if mid != nil {
		midStatus = mid.Status
	}
	r.mu.RUnlock()
	assert.Equal(t, AppStatusStopping, midStatus,
		"long-stop budget not honored: app transitioned out of STOPPING before 3s stopDelay elapsed")

	// The stop should complete at ~3s; T21 routes to READY_TO_PULL →
	// PULLING → READY_TO_START → STARTING → RUNNING. Allow generous
	// headroom for the state-machine walk.
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not reach RUNNING via initial-sweep recreate, got %s", status)
	}

	// Verify the recreate path did fire: stopContainer must have run during
	// the sweep. (Pull is not required — simpleApp uses KindThirdParty with
	// imageExists=true, so T3 skips pull and transitions straight to
	// READY_TO_START. The sweep itself is the load-bearing side effect.)
	md.mu.Lock()
	stopsAfter := md.stopRemoveCalls
	md.mu.Unlock()
	_ = pullsBefore
	assert.Greater(t, stopsAfter, stopsBefore, "expected stopContainer to fire during sweep")

	// ----- Part 2: contrast with operator-initiated StopApp (initialSweep=false).
	//
	// T23 per-app budget for non-initialSweep = resolveStopTimeout + groupTimeout.
	// With groupTimeout=1s and no app-level StopTimeout (resolves to
	// defaultStopTimeout=1), budget = 1s + 1s = 2s. stopDelay=3s > budget
	// → T23 fires → STOPPING_FAILED.

	require.NoError(t, r.StopApp(def.Name))

	// Wait for T23 to fire. Budget is 1s, tick period 1s, so expect
	// STOPPING_FAILED around ~2–3s. Give 5s headroom.
	deadline = time.Now().Add(5 * time.Second)
	var stoppingFailed bool
	for time.Now().Before(deadline) {
		app := r.FindApp(def.Name)
		if app != nil && app.Status == AppStatusStoppingFailed {
			stoppingFailed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !stoppingFailed {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("operator-initiated StopApp did NOT route to STOPPING_FAILED under per-app budget (got %s); "+
			"budget=resolveStopTimeout+groupTimeout = 1s+1s = 2s; stopDelay=3s should exceed it", status)
	}

	// ----- Sanity: make sure we haven't deadlocked the runtime by shutting down.
	// Clear the delay so the deferred Shutdown() path doesn't hang on
	// leftover docker stop delays.
	md.mu.Lock()
	md.stopDelay = 0
	md.mu.Unlock()
}

// TestStopAppDuringInitialSweepDetaches pins the StopApp-during-sweep race
// contract: when doStart enters an app as STOPPING+initialSweep=true+
// desiredNext=READY_TO_PULL (stale-container sweep), a concurrent StopApp
// MUST take precedence. handleStopResult T21 checks manualStoppedApps before
// honoring desiredNext — the user's detach intent overrides the sweep's
// routing-back-up intent.
//
// Without the fix, the app would silently route to READY_TO_PULL → ... →
// RUNNING and the user's stop would be lost.
func TestStopAppDuringInitialSweepDetaches(t *testing.T) {
	md := newMockDocker()

	// stopBlock holds the sweep's stopContainer worker mid-flight so we have a
	// deterministic window to fire StopApp while the app is STOPPING+initialSweep.
	md.stopBlock = make(chan struct{})

	// Seed a stale container (mismatched hash) so doStart routes through the
	// stale-sweep path (STOPPING + initialSweep=true + desiredNext=READY_TO_PULL).
	def := simpleApp("postgres", "postgres:17")
	md.mu.Lock()
	md.nextID++
	md.containers[def.Name] = mockContainer{
		id: "container-stale",
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: "stale-hash-does-not-match",
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	r := NewRuntime(testConfig(), md, t.TempDir())
	// Generous long-stop budget so the 1s groupTimeout can't preempt us into
	// STOPPING_FAILED while we wait for the StopApp race window.
	r.groupTimeout = 10 * time.Second
	r.longStopTimeout = 30 * time.Second
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{def})

	// Wait until the app enters STOPPING with initialSweep=true (doStart has
	// run, stopContainer worker has been dispatched and is now blocked on
	// md.stopBlock).
	var sawInitialSweep bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		app, ok := r.apps[def.Name]
		if ok && app.Status == AppStatusStopping && app.initialSweep {
			sawInitialSweep = true
			r.mu.RUnlock()
			break
		}
		r.mu.RUnlock()
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, sawInitialSweep,
		"app did not enter STOPPING+initialSweep before StopApp race window")

	// Pre-release sanity: desiredNext is READY_TO_PULL (the sweep wants to
	// route back up after the stop completes). The manualStoppedApps map is
	// empty (no user intent yet).
	r.mu.RLock()
	preDesired := r.apps[def.Name].desiredNext
	_, preDetached := r.manualStoppedApps[def.Name]
	r.mu.RUnlock()
	require.Equal(t, AppStatusReadyToPull, preDesired,
		"stale-sweep must seed desiredNext=READY_TO_PULL")
	require.False(t, preDetached,
		"manualStoppedApps must be empty before StopApp is called")

	// Race window: fire StopApp while the sweep's stopContainer is still
	// blocked on md.stopBlock. StopApp's STOPPING branch records
	// manualStoppedApps[appName]=true (the only thing it does in this branch).
	require.NoError(t, r.StopApp(def.Name))

	// Confirm manualStoppedApps now records the user's detach intent.
	r.mu.RLock()
	_, detached := r.manualStoppedApps[def.Name]
	r.mu.RUnlock()
	require.True(t, detached,
		"StopApp during STOPPING must set manualStoppedApps=true")

	// Unblock the stopContainer worker; T21 fires.
	close(md.stopBlock)

	// Without the fix: app routes desiredNext=READY_TO_PULL → PULLING → ...
	//                   → RUNNING. manualStoppedApps stays but is silently ignored.
	// With the fix: T21 sees manualStoppedApps[app.Name]==true BEFORE honoring
	//               desiredNext, clears desiredNext, and routes to STOPPED.
	if !waitForAppStatus(r, def.Name, AppStatusStopped, 5*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not reach STOPPED after StopApp-during-sweep (got %s); "+
			"user's detach intent was silently overridden by sweep's desiredNext=READY_TO_PULL",
			status)
	}

	// Final invariants: app is STOPPED, manualStoppedApps still records the
	// detach (survives T21), and desiredNext was cleared (no pending routing).
	r.mu.RLock()
	final := r.apps[def.Name]
	finalStatus := final.Status
	finalDesired := final.desiredNext
	_, finalDetached := r.manualStoppedApps[def.Name]
	r.mu.RUnlock()
	assert.Equal(t, AppStatusStopped, finalStatus,
		"app final status must be STOPPED")
	assert.True(t, finalDetached,
		"manualStoppedApps must persist across T21 (detach intent survives)")
	assert.Equal(t, AppRuntimeStatus(""), finalDesired,
		"desiredNext must be cleared after T21 honored manualStoppedApps")
}
