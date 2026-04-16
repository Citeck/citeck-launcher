// Edge-case regression tests:
//   - TestRestartAppDuringShutdownIsRejected — RestartApp during NS STOPPING
//     must return an error; without it, desiredNext=READY_TO_PULL would stall
//     allAppsTerminalInGroup and deadlock r.wg.Wait().
//   - TestPullTaskStallCancelsAttempt — pull-stall watchdog fires correctly
//     when PullImageWithProgress makes no progress.
//   - TestPostStartActionsWorkerIsDispatchedAfterRunning — OpPostStartActions
//     worker is dispatched asynchronously so runtimeLoop stays responsive while
//     exec blocks; ExecInContainer is called for each app's init actions.
package namespace

import (
	"context"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartAppDuringShutdownIsRejected pins the guard against a deadlock: a
// RestartApp call while the namespace is in NsStatusStopping must be rejected
// with a clear error. Without it, desiredNext=READY_TO_PULL stalls
// allAppsTerminalInGroup — see the guard comment in RestartApp for the full
// chain.
func TestRestartAppDuringShutdownIsRejected(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	// Wedge stop so NS stays in STOPPING long enough for the RestartApp racer
	// to observe the guard.
	md.mu.Lock()
	md.stopBlock = make(chan struct{})
	md.mu.Unlock()
	defer func() {
		md.mu.Lock()
		if md.stopBlock != nil {
			close(md.stopBlock)
			md.stopBlock = nil
		}
		md.mu.Unlock()
	}()

	r.Stop()

	// Wait for NS STOPPING.
	require.True(t, waitForStatus(r, NsStatusStopping, 5*time.Second),
		"namespace did not reach STOPPING")

	// Now race a RestartApp — must be rejected.
	err := r.RestartApp("postgres")
	require.Error(t, err, "RestartApp during NS STOPPING must return an error")
	assert.Contains(t, err.Error(), "stopping",
		"RestartApp error should mention 'stopping', got: %v", err)

	// Release stop; shutdown must complete without hanging.
	md.mu.Lock()
	if md.stopBlock != nil {
		close(md.stopBlock)
		md.stopBlock = nil
	}
	md.mu.Unlock()

	require.True(t, waitForStatus(r, NsStatusStopped, 5*time.Second),
		"namespace did not reach STOPPED after releasing stop (possible deadlock)")
}

// TestPullTaskStallCancelsAttempt pins the pull-stall watchdog: it must fire
// when PullImageWithProgress makes no progress, producing a "no progress"
// error. The test overrides pullStallTimeout / pullStallPollInterval to short
// values so the watchdog fires within milliseconds; the whole test completes in
// well under 2 seconds. An empty retryDelays slice ensures runPullTask returns
// after the single stalled attempt instead of looping through the full backoff
// schedule.
func TestPullTaskStallCancelsAttempt(t *testing.T) {
	// Save and restore stall knobs so parallel tests are not affected.
	oldTimeout := pullStallTimeout
	oldPoll := pullStallPollInterval
	pullStallTimeout = 100 * time.Millisecond
	pullStallPollInterval = 20 * time.Millisecond
	defer func() {
		pullStallTimeout = oldTimeout
		pullStallPollInterval = oldPoll
	}()

	md := newWorkerMockDocker()
	md.imageExists = false
	// pullBlock is set but never closed — PullImageWithProgress blocks until its
	// context (stallCtx) is canceled by the watchdog.
	md.pullBlock = make(chan struct{})

	r := newWorkerTestRuntime(t, md)

	start := time.Now()
	// Empty retryDelays: single attempt then return — confirms watchdog fires
	// on the first try and produces the "no progress" error text without
	// entering the retry backoff loop.
	res := r.runPullTask(context.Background(), "nexus.citeck.ru/img:stall", true, []time.Duration{}, nil)
	elapsed := time.Since(start)

	require.Error(t, res.Err, "stall watchdog must produce an error")
	assert.Contains(t, res.Err.Error(), "no progress",
		"error must mention 'no progress'; got: %v", res.Err)
	assert.Less(t, elapsed, 2*time.Second,
		"watchdog must fire well within the test timeout; elapsed=%v", elapsed)

	// Confirm the watchdog goroutine exited cleanly: the watchdogDone channel
	// is closed before runPullTask returns (via <-watchdogDone in the loop),
	// so reaching here means no goroutine leak.
	md.mu.Lock()
	pulls := md.pullCalls
	md.mu.Unlock()
	assert.Equal(t, 1, pulls, "exactly one pull invocation before stall detection")
}

// TestPostStartActionsWorkerIsDispatchedAfterRunning pins the async dispatch
// contract for OpPostStartActions: the worker must be dispatched
// asynchronously — runtimeLoop must remain responsive to other apps'
// handleStartResult calls while exec is blocked on the first app.
//
// Shape: two apps (app-a, app-b), both with an InitAction.  execBlock is set
// BEFORE Start so the very first ExecInContainer call blocks.  app-a's
// handleStartResult fires first and commits RUNNING + dispatches its worker.
// If the worker were inline (not async), runtimeLoop would block waiting for
// exec to complete and app-b's handleStartResult would never run → app-b
// would be stuck in STARTING indefinitely.  The test asserts app-b reaches
// RUNNING within 3 s while exec is still blocked, which is impossible unless
// the dispatch is truly asynchronous.
//
// Mutation coverage: replacing the goroutine dispatch with an inline call to
// r.runPostStartActionsTask at T15/T16 would cause app-b to hang at STARTING,
// failing the assert below.
func TestPostStartActionsWorkerIsDispatchedAfterRunning(t *testing.T) {
	md := newMockDocker()
	// Block ALL ExecInContainer calls so both workers stall until we unblock.
	execUnblock := make(chan struct{})
	md.execBlock = execUnblock

	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	defer r.Shutdown()

	initAction := appdef.AppInitAction{Exec: []string{"echo", "hello"}}
	apps := []appdef.ApplicationDef{
		{
			Name:  "app-a",
			Image: "postgres:17",
			Kind:  appdef.KindThirdParty,
			Resources: &appdef.AppResourcesDef{
				Limits: appdef.LimitsDef{Memory: "256m"},
			},
			InitActions: []appdef.AppInitAction{initAction},
		},
		{
			Name:  "app-b",
			Image: "postgres:17",
			Kind:  appdef.KindThirdParty,
			Resources: &appdef.AppResourcesDef{
				Limits: appdef.LimitsDef{Memory: "256m"},
			},
			InitActions: []appdef.AppInitAction{initAction},
		},
	}
	r.Start(apps)

	// Wait for app-a RUNNING — its worker started and is now blocked on exec.
	require.True(t, waitForAppStatus(r, "app-a", AppStatusRunning, 10*time.Second),
		"app-a did not reach RUNNING")

	// While exec is still blocked, app-b must also reach RUNNING within 3s.
	// If the dispatch were inline, runtimeLoop would be stuck on app-a's exec
	// and would never process app-b's handleStartResult.
	require.True(t, waitForAppStatus(r, "app-b", AppStatusRunning, 3*time.Second),
		"app-b did not reach RUNNING while app-a's exec is blocked — "+
			"post-start worker dispatch is likely not async")

	// Release exec; both workers should complete and exec count must be ≥ 2.
	close(execUnblock)

	require.Eventually(t, func() bool {
		md.mu.Lock()
		defer md.mu.Unlock()
		return md.execCalls >= 2
	}, 5*time.Second, 20*time.Millisecond,
		"ExecInContainer must be called at least twice (once per app init action)")
}
