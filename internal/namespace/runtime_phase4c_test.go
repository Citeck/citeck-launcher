// Behavioral tests for the state-machine stop/liveness/retry transitions:
// T17a (liveness threshold → STOPPING), T18 (reconciler-detected crash/oom →
// READY_TO_PULL), T19/T19b/T19c (cmdStopApp variants by source state), T21/T22
// (stop OK / err), T26 (cmdRetryPullFailed), T27/T28/T29/T30 (cmdStartApp
// variants), T33 (re-adopt WARN observability).
package namespace

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stopErrorDocker wraps mockDocker so StopAndRemoveContainer fails. Used by
// TestStoppingFailedDiscardsDesiredNext and the T22/T30 paths.
type stopErrorDocker struct {
	*mockDocker
	stopErr error
}

func (d *stopErrorDocker) StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error {
	d.mu.Lock()
	d.stopRemoveCalls++
	d.mu.Unlock()
	if d.stopErr != nil {
		return d.stopErr
	}
	return d.mockDocker.StopAndRemoveContainer(ctx, name, timeoutSec)
}

// waitFor polls cond until it returns true or deadline elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// TestReconcilerRestartPinnedImageDoesNotPull exercises T18: when the
// reconciler detects a missing container for a pinned ThirdParty app, the app
// transitions to READY_TO_PULL → T3 (image local + !shouldPullImage) →
// READY_TO_START → … → RUNNING. The pull worker must NOT fire because
// shouldPullImage(KindThirdParty, _) returns false.
func TestReconcilerRestartPinnedImageDoesNotPull(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	require.Equal(t, appdef.KindThirdParty, def.Kind, "test requires KindThirdParty for !shouldPullImage path")
	r.Start([]appdef.ApplicationDef{def})

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}
	// Also wait for namespace to reach RUNNING. reconcile() early-returns when
	// NS status ≠ RUNNING and ≠ STALLED (reconciler.go:78). Under CPU contention
	// updateNsStatus can lag after the app reaches RUNNING — calling reconcile()
	// synchronously while NS is still STARTING makes it a no-op and the test
	// panics at restartEvents[-1].
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING — reconcile() would be a no-op")
	}

	// Snapshot the pre-T18 pull count so we can assert no new pulls happen.
	md.mu.Lock()
	pullsBefore := md.pullCalls
	md.mu.Unlock()

	// Simulate container disappearance: delete from mock and trigger reconcile.
	md.mu.Lock()
	delete(md.containers, def.Name)
	md.mu.Unlock()

	// Run the reconciler manually — same pattern as TestCheckLivenessFailureCounting.
	r.reconcileOnce(context.Background())

	// Wait for the app to recover via T18 → T3 → ... → RUNNING.
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not recover to RUNNING after T18, got %s", status)
	}

	md.mu.Lock()
	pullsAfter := md.pullCalls
	md.mu.Unlock()
	assert.Equal(t, pullsBefore, pullsAfter,
		"T18 + T3 must not re-pull a pinned ThirdParty image (before=%d after=%d)", pullsBefore, pullsAfter)

	// One restart_event with reason=crash recorded.
	r.mu.RLock()
	defer r.mu.RUnlock()
	require.GreaterOrEqual(t, len(r.restartEvents), 1, "expected at least one restart_event for the crash")
	assert.Equal(t, "crash", r.restartEvents[len(r.restartEvents)-1].Reason)
}

// TestCmdStopAppWhilePulling exercises T19b: an app in PULLING when StopApp
// arrives. The app transitions directly to STOPPED (no stopContainer
// dispatched), the pull worker is canceled, and manualStoppedApps records the
// detach intent.
func TestCmdStopAppWhilePulling(t *testing.T) {
	md := newMockDocker()
	md.imageExists = map[string]bool{"ecos-model:2.0": false} // forces T2 pull dispatch
	md.pullBlock = make(chan struct{})

	r := NewRuntime(testConfig(), md, t.TempDir())
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(md.pullBlock) }) }
	defer func() {
		unblock()
		r.Shutdown()
	}()

	def := simpleApp("emodel", "ecos-model:2.0")
	r.Start([]appdef.ApplicationDef{def})

	if !waitForAppStatus(r, def.Name, AppStatusPulling, 5*time.Second) {
		t.Fatalf("app did not reach PULLING for T19b setup")
	}

	stopRemoveBefore := md.stopRemoveCalls

	// T19b: cmdStopApp on a PULLING app — no container exists.
	require.NoError(t, r.StopApp(def.Name))

	// App must transition directly to STOPPED.
	if !waitForAppStatus(r, def.Name, AppStatusStopped, 5*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not reach STOPPED via T19b, got %s", status)
	}

	// Detach intent persisted.
	r.mu.RLock()
	_, detached := r.manualStoppedApps[def.Name]
	r.mu.RUnlock()
	assert.True(t, detached, "T19b must record manualStoppedApps")

	// Unblock the pull so its goroutine returns; assert no stopContainer was
	// dispatched (T19b transitions to STOPPED without stop dispatch).
	unblock()
	time.Sleep(200 * time.Millisecond)
	md.mu.Lock()
	stopRemoveAfter := md.stopRemoveCalls
	md.mu.Unlock()
	assert.Equal(t, stopRemoveBefore, stopRemoveAfter,
		"T19b must not dispatch stopContainer (before=%d after=%d)", stopRemoveBefore, stopRemoveAfter)
}

// TestStoppingFailedDiscardsDesiredNext exercises T22: when stopContainer
// fails on a STOPPING app whose desiredNext was set (e.g., T17a-driven
// liveness restart), the app transitions to STOPPING_FAILED and desiredNext
// is cleared — the restart intent is discarded. Without that clear, T21's
// "apply desiredNext" path would erroneously route to READY_TO_PULL on a
// retry.
func TestStoppingFailedDiscardsDesiredNext(t *testing.T) {
	mdReal := newMockDocker()
	md := &stopErrorDocker{mockDocker: mdReal}
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Configure stopContainer to fail.
	md.stopErr = context.DeadlineExceeded

	// Simulate a T17a transition: set desiredNext + STOPPING + dispatch
	// stopContainer. The dispatch fails → handleStopResult applies T22.
	r.mu.Lock()
	app := r.apps[def.Name]
	app.desiredNext = AppStatusReadyToPull
	app.stoppingStartedAt = r.nowFunc()
	containerName := r.docker.ContainerName(def.Name)
	r.setAppStatus(app, AppStatusStopping)
	plan := r.makeStopPlan(def.Name, containerName, 0)
	r.mu.Unlock()
	r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	r.signalCh.Flush()

	// Wait for STOPPING_FAILED (T22).
	if !waitForAppStatus(r, def.Name, AppStatusStoppingFailed, 5*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not reach STOPPING_FAILED via T22, got %s", status)
	}

	// desiredNext must be cleared.
	r.mu.RLock()
	gotDesiredNext := r.apps[def.Name].desiredNext
	r.mu.RUnlock()
	assert.Equal(t, AppRuntimeStatus(""), gotDesiredNext,
		"T22 must clear desiredNext (got %q)", gotDesiredNext)

	// And the app must NOT auto-promote out of STOPPING_FAILED — T31 is
	// no-auto-retry by design. Wait one ticker cycle + slack.
	time.Sleep(1500 * time.Millisecond)
	app2 := r.FindApp(def.Name)
	require.NotNil(t, app2)
	assert.Equal(t, AppStatusStoppingFailed, app2.Status,
		"STOPPING_FAILED must be sticky (no auto-retry per T31), got %s", app2.Status)
}

// TestCmdStartAppOnStoppingFailedRetriesStop exercises T30: an app stuck in
// STOPPING_FAILED is recovered via cmdStartApp → fresh STOPPING attempt with
// desiredNext=READY_TO_PULL → T21 routes to READY_TO_PULL → start.
func TestCmdStartAppOnStoppingFailedRetriesStop(t *testing.T) {
	mdReal := newMockDocker()
	md := &stopErrorDocker{mockDocker: mdReal}
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Drive into STOPPING_FAILED via the same T22 path as above.
	md.stopErr = context.DeadlineExceeded
	r.mu.Lock()
	app := r.apps[def.Name]
	app.desiredNext = AppStatusReadyToPull
	app.stoppingStartedAt = r.nowFunc()
	containerName := r.docker.ContainerName(def.Name)
	r.setAppStatus(app, AppStatusStopping)
	plan := r.makeStopPlan(def.Name, containerName, 0)
	r.mu.Unlock()
	r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	r.signalCh.Flush()

	if !waitForAppStatus(r, def.Name, AppStatusStoppingFailed, 5*time.Second) {
		t.Fatalf("app did not reach STOPPING_FAILED for T30 setup")
	}

	// Repair the docker: stop will succeed on the next attempt.
	md.stopErr = nil

	// T30: cmdStartApp on STOPPING_FAILED → STOPPING with fresh dispatch.
	require.NoError(t, r.StartApp(def.Name))

	// The fresh stop succeeds → T21 routes to READY_TO_PULL → ... → RUNNING.
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("T30 should drive STOPPING_FAILED → ... → RUNNING, got %s", status)
	}
}

// TestReAdoptFailingContainerEmitsWarning exercises T33: when an app is
// adopted (T1) and its most-recent restart_event has reason in
// {liveness, crash, oom}, doStart emits a `readopted_failing` event.
func TestReAdoptFailingContainerEmitsWarning(t *testing.T) {
	md := newMockDocker()

	def := simpleApp("postgres", "postgres:17")
	hashDef := def
	hashDef.ImageDigest = "sha256:mock-digest-" + def.Image

	// Seed an existing container with a hash matching what doStart will compute.
	md.mu.Lock()
	md.nextID++
	md.containers[def.Name] = mockContainer{
		id: "container-adopted",
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: hashDef.GetHash(),
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Pre-seed a "liveness" restart_event so T33 sees a bad reason on adoption.
	r.mu.Lock()
	r.restartEvents = append(r.restartEvents, RestartEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		App:       def.Name,
		Reason:    "liveness",
	})
	r.mu.Unlock()

	// Capture restart_event SSE events.
	var (
		mu       sync.Mutex
		readopt  int
		liveness int
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "restart_event" || evt.AppName != def.Name {
			return
		}
		mu.Lock()
		switch evt.After {
		case "readopted_failing":
			readopt++
		case "liveness":
			liveness++
		}
		mu.Unlock()
	})

	r.Start([]appdef.ApplicationDef{def})
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Wait for the readopted_failing event to land.
	ok := waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return readopt >= 1
	})
	mu.Lock()
	gotReadopt := readopt
	mu.Unlock()
	if !ok {
		t.Fatalf("T33 did not emit readopted_failing (got readopted_failing=%d)", gotReadopt)
	}
	assert.Equal(t, 1, gotReadopt, "expected exactly one readopted_failing event")
}

// TestT33FiresExactlyOnceNotRepeatedly exercises T33's self-mute: after
// readopted_failing is appended for app X, the next reverse-scan returns
// readopted_failing (not in the bad-set), so a subsequent adoption MUST NOT
// emit a duplicate.
func TestT33FiresExactlyOnceNotRepeatedly(t *testing.T) {
	md := newMockDocker()
	def := simpleApp("postgres", "postgres:17")
	hashDef := def
	hashDef.ImageDigest = "sha256:mock-digest-" + def.Image

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Pre-seed restart_events: liveness, then readopted_failing (simulating a
	// prior daemon's T33). The most-recent reason for the app is now
	// "readopted_failing", so a subsequent T33 check must return clean.
	r.mu.Lock()
	r.restartEvents = []RestartEvent{
		{App: def.Name, Reason: "liveness"},
		{App: def.Name, Reason: "readopted_failing"},
	}
	r.mu.Unlock()

	// Confirm the helper agrees: lastRestartReason must return readopted_failing.
	r.mu.RLock()
	got := r.lastRestartReason(def.Name)
	r.mu.RUnlock()
	require.Equal(t, "readopted_failing", got, "lastRestartReason must return the most-recent reason")

	// Seed an adoptable container.
	md.mu.Lock()
	md.nextID++
	md.containers[def.Name] = mockContainer{
		id: "container-adopted",
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: hashDef.GetHash(),
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	// Capture readopted_failing events.
	var (
		mu   sync.Mutex
		seen int
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type == "restart_event" && evt.After == "readopted_failing" && evt.AppName == def.Name {
			mu.Lock()
			seen++
			mu.Unlock()
		}
	})

	r.Start([]appdef.ApplicationDef{def})
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Give the loop a chance to dispatch the (non-)event.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	gotSeen := seen
	mu.Unlock()
	assert.Zero(t, gotSeen, "T33 must self-mute when lastRestartReason is readopted_failing (saw %d events)", gotSeen)
}

// TestCmdStopWhilePulling is the namespace-wide cmdStop variant of T20b: an
// app in PULLING when cmdStop arrives must transition to STOPPED without a
// stopContainer dispatch. T20b (beginGroupStopUnderLock) cancels the pull
// worker and calls setAppStatus(app, STOPPED) directly — the per-app STOPPED
// event is observable via SetEventCallback before the namespace reaches STOPPED.
func TestCmdStopWhilePulling(t *testing.T) {
	md := newMockDocker()
	md.imageExists = map[string]bool{"ecos-model:2.0": false}
	md.pullBlock = make(chan struct{})

	r := NewRuntime(testConfig(), md, t.TempDir())
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(md.pullBlock) }) }
	defer unblock()

	// Subscribe before Stop so we observe the per-app STOPPED transition
	// emitted by T20b (PULLING → STOPPED via beginGroupStopUnderLock).
	var (
		evMu           sync.Mutex
		appStoppedSeen bool
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type == "app_status" && evt.AppName == "emodel" && evt.After == string(AppStatusStopped) {
			evMu.Lock()
			appStoppedSeen = true
			evMu.Unlock()
		}
	})

	def := simpleApp("emodel", "ecos-model:2.0")
	r.Start([]appdef.ApplicationDef{def})

	if !waitForAppStatus(r, def.Name, AppStatusPulling, 5*time.Second) {
		t.Fatalf("app did not reach PULLING for cmdStop setup")
	}

	stopRemoveBefore := md.stopRemoveCalls

	// Issue full cmdStop. T20b cancels the pull worker and emits STOPPED for
	// the containerless PULLING app without dispatching stopContainer.
	r.Stop()
	if !waitForStatus(r, NsStatusStopped, 10*time.Second) {
		t.Fatalf("namespace did not reach STOPPED via cmdStop while PULLING")
	}

	// T20b must have emitted a per-app STOPPED event for "emodel".
	evMu.Lock()
	sawAppStopped := appStoppedSeen
	evMu.Unlock()
	assert.True(t, sawAppStopped,
		"T20b must emit app_status STOPPED for the PULLING (container-less) app")

	// No new stopContainer for the PULLING (container-less) app.
	md.mu.Lock()
	stopRemoveAfter := md.stopRemoveCalls
	md.mu.Unlock()
	assert.Equal(t, stopRemoveBefore, stopRemoveAfter,
		"cmdStop must not dispatch stopContainer for a PULLING (container-less) app")
	unblock()
}

// TestCmdStopWhileStarting is the namespace-wide cmdStop variant of T20: an
// app in RUNNING with a live ContainerID must transition through STOPPING →
// STOPPED when cmdStop arrives. T20 routes RUNNING apps through
// beginGroupStopUnderLock (STOPPING + stopContainer dispatch), then T21
// (handleStopResult OK → STOPPED). Both per-app transitions are observable
// via SetEventCallback.
func TestCmdStopWhileStarting(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Subscribe before Stop to observe the STOPPING → STOPPED per-app chain.
	var (
		evMu           sync.Mutex
		sawAppStopping bool
		sawAppStopped  bool
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "app_status" || evt.AppName != def.Name {
			return
		}
		evMu.Lock()
		defer evMu.Unlock()
		if evt.After == string(AppStatusStopping) {
			sawAppStopping = true
		}
		if evt.After == string(AppStatusStopped) {
			sawAppStopped = true
		}
	})

	stopRemoveBefore := md.stopRemoveCalls

	r.Stop()
	if !waitForStatus(r, NsStatusStopped, 10*time.Second) {
		t.Fatalf("namespace did not reach STOPPED")
	}

	// T20 must have emitted STOPPING then T21 must have emitted STOPPED.
	evMu.Lock()
	stopping, stopped := sawAppStopping, sawAppStopped
	evMu.Unlock()
	assert.True(t, stopping, "T20 must emit app_status STOPPING for the container-bearing app")
	assert.True(t, stopped, "T21 must emit app_status STOPPED after stopContainer succeeds")

	md.mu.Lock()
	stopRemoveAfter := md.stopRemoveCalls
	md.mu.Unlock()
	assert.Greater(t, stopRemoveAfter, stopRemoveBefore,
		"cmdStop must dispatch stopContainer for a RUNNING (container-bearing) app")
}

// TestCmdStopWhileInitContainersRunning exercises T19c — the STARTING-init
// phase variant of cmdStopApp. Scenario:
//  1. App with an init container.
//  2. mockDocker.initContainerWaitBlock pins the init worker at
//     WaitForContainerExit, leaving app.Status==STARTING with
//     app.ContainerID=="" (init-phase sentinel).
//  3. r.StopApp(app) → T19c: cancel init worker, dispatch stopContainer
//     on "{appName}-init".
//  4. The canceled init worker observes ctx.Done in the mock, exits via
//     cleanup. T21 on the {appName}-init stopContainer Result routes the
//     app (desiredNext cleared by T19c) straight to STOPPED.
//
// Asserts: app reaches STOPPED; manualStoppedApps[name]=true; at least
// one stopContainer dispatch hit the init container name.
func TestCmdStopWhileInitContainersRunning(t *testing.T) {
	md := newMockDocker()
	md.initContainerWaitBlock = make(chan struct{})

	r := NewRuntime(testConfig(), md, t.TempDir())
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(md.initContainerWaitBlock) }) }
	defer func() {
		unblock()
		r.Shutdown()
	}()

	def := simpleApp("webapp", "webapp:1")
	def.InitContainers = []appdef.InitContainerDef{
		{Image: "init-img:1"},
	}
	r.Start([]appdef.ApplicationDef{def})

	// Wait for the init worker to enter the blocked WaitForContainerExit,
	// meaning app is in STARTING with ContainerID=="" (init-phase).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app := r.FindApp(def.Name)
		if app != nil && app.Status == AppStatusStarting && app.ContainerID == "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	app := r.FindApp(def.Name)
	require.NotNil(t, app, "app must be registered")
	require.Equal(t, AppStatusStarting, app.Status, "app must be in STARTING for T19c setup")
	require.Empty(t, app.ContainerID, "ContainerID must be empty (init-phase) for T19c setup")

	// Verify the init container was created and registered in the mock —
	// proves the worker reached WaitForContainerExit (and is now blocked).
	initMockName := def.Name + "-init"
	md.mu.Lock()
	_, initCreated := md.containers[initMockName]
	stopRemoveBefore := md.stopRemoveCalls
	md.mu.Unlock()
	require.True(t, initCreated, "init container must have been created by runInitContainerTask")

	// T19c: cancel init worker + dispatch stopContainer on "{appName}-init".
	err := r.StopApp(def.Name)
	require.NoError(t, err, "StopApp during STARTING(init-phase) must succeed")

	// App should reach STOPPED via T21 on the init-name stopContainer Result.
	// desiredNext was cleared by T19c, so T21 routes directly to STOPPED.
	require.True(t, waitForAppStatus(r, def.Name, AppStatusStopped, 5*time.Second),
		"app must reach STOPPED after T19c (cancel init + stop init container)")

	// At least one additional stopContainer dispatch hit the init container.
	md.mu.Lock()
	stopRemoveAfter := md.stopRemoveCalls
	_, initStillExists := md.containers[initMockName]
	md.mu.Unlock()
	assert.Greater(t, stopRemoveAfter, stopRemoveBefore,
		"T19c must dispatch at least one stopContainer (on the init container name)")
	assert.False(t, initStillExists,
		"init container %q must be removed by the T19c stopContainer dispatch", initMockName)

	// manualStoppedApps records the detach intent.
	assert.True(t, r.ManualStoppedApps()[def.Name],
		"StopApp must mark app as manually stopped")

	// Unblock the canceled init worker so Shutdown can drain cleanly.
	unblock()
}
