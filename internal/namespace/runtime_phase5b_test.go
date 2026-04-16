// Behavioral tests for the state-machine-driven graceful shutdown chain.
// doStop is non-blocking: it transitions group[0] apps to STOPPING, dispatches
// their stopContainer workers, and registers a cmdStopNextGroup continuation.
// The continuation chain walks groups 1..N, then dispatches RemoveNetworkTask.
// handleRemoveNetworkResult finalizes NsStatusStopped and signals
// shutdownComplete.
package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStopGroupTimeoutProceedsToNextGroup pins the T23 + continuation-chain
// contract: an app in group[0] that fails to exit within groupTimeout is
// routed to STOPPING_FAILED by tick(), which satisfies
// allAppsTerminalInGroup(0) → cmdStopNextGroup{1,...} fires → group[1] apps
// proceed to STOPPED → RemoveNetwork → NS STOPPED.
func TestStopGroupTimeoutProceedsToNextGroup(t *testing.T) {
	md := newMockDocker()

	r := NewRuntime(testConfig(), md, t.TempDir())
	// Short budgets so T23 trips promptly. defaultStopTimeout=1 ensures
	// per-app budget = 1s + groupTimeout = 1.3s (not 10s Docker default).
	r.groupTimeout = 300 * time.Millisecond
	r.longStopTimeout = 1 * time.Second
	r.defaultStopTimeout = 1
	// Shorten tick so T23 fires within the test window.
	r.tickerPeriod = 50 * time.Millisecond
	defer r.Shutdown()

	// Two apps: proxy (group[0]) and postgres (group[infra] — last group).
	// Both get stopBlock'd; proxy's T23 triggers STOPPING_FAILED which lets
	// the chain advance past group[0].
	apps := []appdef.ApplicationDef{
		simpleApp(appdef.AppProxy, "nginx:1.27"),
		simpleApp(appdef.AppPostgres, "postgres:17"),
	}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	// After startup, wedge stopContainer so the graceful groups can't
	// complete. Install the block AFTER RUNNING so runStartTask's best-effort
	// pre-create cleanup isn't affected.
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

	// Trigger shutdown.
	r.Stop()

	// proxy should hit STOPPING_FAILED via T23 (budget exhausted).
	require.True(t, waitForAppStatus(r, appdef.AppProxy, AppStatusStoppingFailed, 5*time.Second),
		"proxy did not reach STOPPING_FAILED under short groupTimeout")

	// postgres should also be STOPPING (or STOPPING_FAILED after T23); the
	// chain must advance regardless.
	require.True(t, waitForAppStatus(r, appdef.AppPostgres, AppStatusStoppingFailed, 5*time.Second),
		"postgres did not reach STOPPING_FAILED (chain should advance)")

	// Namespace must still reach STOPPED even though all apps failed their
	// stop — RemoveNetwork is best-effort and runs regardless.
	require.True(t, waitForStatus(r, NsStatusStopped, 5*time.Second),
		"namespace did not reach STOPPED after group-timeout chain")

	// RemoveNetwork should have been invoked exactly once.
	md.mu.Lock()
	netCalls := md.removeNetCalls
	md.mu.Unlock()
	assert.Equal(t, 1, netCalls, "RemoveNetwork should run exactly once after all groups drain")
}

// TestStopReleasesLockDuringNetworkRemoval verifies that RemoveNetwork runs
// in a worker without holding r.mu. Apps() / Status() must remain responsive
// while the network call is in flight. r.mu is only taken briefly in
// handleRemoveNetworkResult, not across the entire Docker call.
func TestStopReleasesLockDuringNetworkRemoval(t *testing.T) {
	md := newMockDocker()
	// Block RemoveNetwork so we have a deterministic window where the worker
	// is running but its Result has not yet arrived.
	md.removeNetBlock = make(chan struct{})

	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	defer r.Shutdown()

	// Simple single-app namespace so the chain reaches RemoveNetwork fast.
	apps := []appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	r.Stop()

	// Wait until RemoveNetwork has been invoked (worker is in flight on
	// md.removeNetBlock). Polling the counter avoids coupling to the
	// state-machine's internal event ordering.
	deadline := time.Now().Add(5 * time.Second)
	for {
		md.mu.Lock()
		called := md.removeNetCalls
		md.mu.Unlock()
		if called > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RemoveNetwork was not dispatched within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Assertion: with RemoveNetwork blocked, Apps() and Status() must
	// still return promptly. Run them a few times against a short deadline —
	// if r.mu were held by the worker, these would block indefinitely.
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		for range 20 {
			_ = r.Apps()
			_ = r.Status()
		}
	}()
	select {
	case <-callDone:
		// Good: readers completed while RemoveNetwork was in flight.
	case <-time.After(1 * time.Second):
		t.Fatal("Apps()/Status() blocked while RemoveNetwork was in flight")
	}

	// Release RemoveNetwork; chain finalizes NsStatusStopped.
	close(md.removeNetBlock)
	require.True(t, waitForStatus(r, NsStatusStopped, 5*time.Second),
		"namespace did not reach STOPPED after releasing RemoveNetwork")
}

// TestShutdownCompleteDoubleCloseSafety pins the sync.Once guard on
// signalShutdown — repeated calls from any goroutine must be a safe no-op.
// Both the stop chain and the cmdDetach path may call signalShutdown.
func TestShutdownCompleteDoubleCloseSafety(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	// Don't Start — we're exercising signalShutdown directly.

	// Two calls must not panic and the channel must be closed exactly once.
	r.signalShutdown()
	r.signalShutdown()
	r.signalShutdown()

	select {
	case <-r.shutdownComplete:
		// Good: channel is closed.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("shutdownComplete was not closed after signalShutdown")
	}

	// r was never Started, so Shutdown should be fast (no runtimeLoop to
	// drain). But we still call it to close eventCh.
	r.Shutdown()
}

// TestDoubleStopDoesNotDuplicateHistory pins the idempotent-doStop invariant:
// a re-entrant doStop (triggered when Stop() is called twice and both
// signals are consumed by runtimeLoop) must NOT register a second
// cmdStopNextGroup continuation. Without the idempotent early-return
// at the top of doStop, the duplicate continuation would:
//   - append a second "stop initiated" OperationHistory entry, and
//   - dispatch RemoveNetwork a second time at the chain's tail.
//
// We gate the chain by blocking RemoveNetwork, fire Stop() twice while
// the chain is in flight, then release the block and assert both the
// Docker-side counter and the history file contain exactly one entry.
func TestDoubleStopDoesNotDuplicateHistory(t *testing.T) {
	md := newMockDocker()
	// Block RemoveNetwork so the shutdown chain is suspended at its tail
	// while we fire the second Stop(). If the fix regresses, a second
	// continuation is registered and a second RemoveNetwork is dispatched
	// after we release the block (md.removeNetCalls == 2).
	md.removeNetBlock = make(chan struct{})

	logDir := t.TempDir()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.SetHistory(NewOperationHistory(logDir))
	r.tickerPeriod = 20 * time.Millisecond
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING")

	// Two Stop() calls. Both enqueue cmdStop; if they arrive in the same
	// Drain batch the queue coalesces them to one, but if the runtimeLoop
	// picks up the first before the second lands they arrive in separate
	// batches and doStop is invoked twice — which is the regression we pin
	// (idempotent doStop under repeated cmdStop).
	r.Stop()
	r.Stop()

	// Wait until RemoveNetwork has been dispatched at least once, so we
	// know the chain has reached its tail and any second doStop would have
	// had its chance to register a duplicate continuation.
	deadline := time.Now().Add(5 * time.Second)
	for {
		md.mu.Lock()
		called := md.removeNetCalls
		md.mu.Unlock()
		if called > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RemoveNetwork was not dispatched within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Give runtimeLoop a generous window to consume any pending cmdStop
	// and (in the regression case) re-enter doStop, register a second
	// continuation, and dispatch a second RemoveNetwork.
	time.Sleep(300 * time.Millisecond)

	// Release RemoveNetwork; chain finalizes NsStatusStopped.
	close(md.removeNetBlock)
	require.True(t, waitForStatus(r, NsStatusStopped, 5*time.Second),
		"namespace did not reach STOPPED after releasing RemoveNetwork")

	// Primary assertion: RemoveNetwork ran exactly once.
	md.mu.Lock()
	netCalls := md.removeNetCalls
	md.mu.Unlock()
	assert.Equal(t, 1, netCalls,
		"RemoveNetwork should be dispatched exactly once across a double Stop()")

	// Secondary assertion: OperationHistory contains exactly one "stop"
	// entry with result "initiated". A duplicate doStop would append a
	// second row.
	data, err := os.ReadFile(filepath.Join(logDir, "operations.jsonl"))
	require.NoError(t, err, "operations.jsonl should exist after Stop()")
	stopInitiated := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		// Cheap substring match — avoids pulling json/encoding into the test
		// just to assert on two fields. OperationRecord serializes op/result
		// as "op":"…","result":"…".
		if strings.Contains(line, `"op":"stop"`) && strings.Contains(line, `"result":"initiated"`) {
			stopInitiated++
		}
	}
	assert.Equal(t, 1, stopInitiated,
		"history should contain exactly one 'stop initiated' entry across a double Stop()")
}

// TestStartAfterStopReinitializesShutdownChan pins the reset block in Start()
// (runtime_commands.go): shutdownComplete / signalOnce / detaching are
// re-initialized on each call so that a second runtimeLoop launched after a
// previous Stop() does not observe an already-closed shutdownComplete and exit
// immediately before processing any commands.
//
// Regression: without the reset block, the second r.Start() would spawn a
// runtimeLoop that exits immediately on the first select (shutdownComplete is
// still closed from the first Stop()), leaving apps stuck in PULLING forever —
// the pull task is dispatched but no loop is running to handle its Result.
func TestStartAfterStopReinitializesShutdownChan(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	// No defer r.Shutdown() here — we drive lifecycle manually and call
	// Shutdown() at the end after the second Start().

	apps := []appdef.ApplicationDef{simpleApp("postgres", "postgres:17")}

	// ----- First lifecycle: Start → RUNNING → Stop → STOPPED.
	r.Start(apps)
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"first Start: namespace did not reach RUNNING")

	r.Stop()
	require.True(t, waitForStatus(r, NsStatusStopped, 10*time.Second),
		"first Stop: namespace did not reach STOPPED")

	// runtimeLoop defers running.Store(false) after the final flushEvents(). The
	// STOPPED status transition fires slightly before runtimeLoop returns, so we
	// must wait for running to clear before calling Start() again — otherwise the
	// CompareAndSwap(false,true) guard in Start() sees running==true and ignores
	// the call (a harmless no-op in production where an HTTP handler fires Start
	// after the daemon fully quiesces, but a real race in this tight test loop).
	require.True(t, waitForRunningFalse(r, 5*time.Second),
		"runtimeLoop did not clear running flag within 5s after STOPPED")

	// ----- Second lifecycle: Start again on the same Runtime instance.
	// The reset block re-creates shutdownComplete / signalOnce / detaching so
	// the new runtimeLoop does NOT observe the closed channel and exit early.
	r.Start(apps)
	require.True(t, waitForAppStatus(r, "postgres", AppStatusRunning, 10*time.Second),
		"second Start: postgres did not reach RUNNING; "+
			"if the reset block in Start() is absent the new runtimeLoop exits immediately "+
			"on the already-closed shutdownComplete channel and the app stays in PULLING forever")

	// Clean up.
	r.Shutdown()
}

// waitForRunningFalse polls r.running until it becomes false or the timeout
// elapses. Required between Stop()+waitForStatus(STOPPED) and a subsequent
// Start() call: STOPPED is set by handleRemoveNetworkResult while runtimeLoop
// is still executing its final flushEvents() before returning — so the
// running flag clears slightly after the status becomes STOPPED.
func waitForRunningFalse(r *Runtime, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !r.running.Load() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}
