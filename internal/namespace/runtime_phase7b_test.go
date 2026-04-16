// Tests for the WaitForInitialReconcile listener fan-out and the single-capture
// pattern in RestartApp:
//   - TestWaitForInitialReconcileAfterRestart — subscribe pattern blocks while
//     STARTING and unblocks on transition to RUNNING.
//   - TestNsStatusListenerFanout — multiple subscribers all receive each
//     transition; slow subscribers drop silently without blocking setStatus.
//   - TestRestartAppAtomicRunCtxCapture — race-detector regression test:
//     RestartApp must capture r.runCtx once under Lock, so concurrent
//     Stop + RestartApp never races on the field itself.
package namespace

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitForInitialReconcileAfterRestart verifies that WaitForInitialReconcile
// returns promptly (via the listener path, not polling) when the namespace
// leaves STARTING. We start a runtime, subscribe WaitForInitialReconcile from
// a separate goroutine BEFORE the namespace reaches RUNNING, and assert the
// wait completes within a short budget once setStatus fans out the transition.
func TestWaitForInitialReconcileAfterRestart(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	apps := []appdef.ApplicationDef{
		simpleApp("postgres", "postgres:17"),
	}
	r.Start(apps)

	// Call WaitForInitialReconcile from a goroutine; it should unblock as soon
	// as setStatus transitions out of STARTING.
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.WaitForInitialReconcile(ctx)
		close(done)
	}()

	// Wait up to 10s for the reconcile wait to return. If WaitForInitialReconcile
	// is still polling at 100ms the test would still pass — but a broken listener
	// wiring (e.g. ch never delivered) would deadlock until ctx fires at 10s,
	// and a regression to pure polling would still work. What this test
	// guarantees is that the subscriber path delivers: we assert ns reached
	// RUNNING (proof setStatus fired) AND WaitForInitialReconcile returned.
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING, got %v", r.Status())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("WaitForInitialReconcile did not return after namespace left STARTING (status=%v)", r.Status())
	}
}

// TestNsStatusListenerFanout verifies that multiple subscribers all receive
// status transitions via the fan-out, and that a slow (unread) subscriber does
// not block setStatus. We drive setStatus directly under Lock to avoid
// coupling to the production Start/Stop timing.
func TestNsStatusListenerFanout(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Two active subscribers + one "slow" subscriber (we never read from it).
	active1 := r.subscribeNsStatus()
	active2 := r.subscribeNsStatus()
	slow := r.subscribeNsStatus()
	defer r.unsubscribeNsStatus(active1)
	defer r.unsubscribeNsStatus(active2)
	defer r.unsubscribeNsStatus(slow)

	// Fill the slow subscriber's buffer past its capacity so the next send
	// must hit the default branch (drop). Time the whole loop to prove
	// setStatus never blocks on a full subscriber — if fan-out used a
	// blocking send, we'd stall indefinitely after cap(slow) iterations.
	iterations := cap(slow) + 2
	start := time.Now()
	r.mu.Lock()
	for range iterations {
		// Directly emit transitions. setStatus skips if status is unchanged,
		// so alternate between two values.
		if r.status == NsStatusStarting {
			r.setStatus(NsStatusRunning)
		} else {
			r.setStatus(NsStatusStarting)
		}
	}
	r.mu.Unlock()
	require.Less(t, time.Since(start), 50*time.Millisecond,
		"setStatus must not block on a full listener buffer")

	// Active subscribers must have received at least one transition each —
	// the fan-out is non-blocking, so even if some events were dropped on a
	// full buffer, at least one recent transition should land in a 4-slot
	// buffer.
	select {
	case <-active1:
	case <-time.After(1 * time.Second):
		t.Fatalf("active1 subscriber did not receive any status transition")
	}
	select {
	case <-active2:
	case <-time.After(1 * time.Second):
		t.Fatalf("active2 subscriber did not receive any status transition")
	}
}

// TestRestartAppAtomicRunCtxCapture is the race-detector regression test that
// fires many concurrent RestartApp calls against a running runtime; the race
// detector must not report any access race on r.runCtx. If a future change
// re-introduces a double-read (re-reading r.runCtx after the initial Lock
// release), this test under `-race` flags it.
//
// Workers storm RestartApp while runtimeLoop is concurrently running tick /
// stepAllApps / handleRestart paths — all of which touch runtime state under
// r.mu. The deferred Shutdown then drives r.Stop() and exercises the
// r.cancel() path after workers finish, preserving the full cancel-surface
// without deadlocking the stop-continuation via late desiredNext writes.
//
// The test does not assert specific status outcomes — RestartApp results
// are inherently nondeterministic under concurrent load. The sole contract
// is: no race on r.runCtx, no panic.
func TestRestartAppAtomicRunCtxCapture(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	// Guard Shutdown with a 10s deadline — if cmdQueue back-pressure or a
	// regression in terminal Stop/Shutdown wiring causes Shutdown to hang,
	// dump all goroutines so the failure is diagnosable instead of a silent
	// test-timeout.
	defer func() {
		shutdownDone := make(chan struct{})
		go func() {
			r.Shutdown()
			close(shutdownDone)
		}()
		select {
		case <-shutdownDone:
		case <-time.After(10 * time.Second):
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("Shutdown did not complete within 10s; goroutines:\n%s", buf[:n])
		}
	}()

	apps := []appdef.ApplicationDef{
		simpleApp("app-a", "image-a:1"),
		simpleApp("app-b", "image-b:1"),
	}
	r.Start(apps)

	// Wait briefly for the runtime to settle into RUNNING before storming it.
	// We don't require RUNNING — only that the initial apps have been
	// registered, so RestartApp won't always hit the "app not found" path.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.FindApp("app-a") != nil && r.FindApp("app-b") != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var wg sync.WaitGroup
	var restartCalls atomic.Int64
	// Pace the storm to stay well under the cmdQueue capacity (256) across
	// the test's lifetime. 4 workers × 10 iters × 2 apps = 80 enqueues —
	// comfortably below 256 even if the runtimeLoop drains at 0 cmd/ms.
	// A 1ms sleep per iteration lets the runtime loop drain between bursts
	// so the 500ms Enqueue back-pressure never fires. The race-detector
	// surface on r.runCtx (RestartApp reads it under Lock; doStop mutates
	// r.cancel under the same Lock) is hit thousands of times when -race
	// runs count=30 — that is the race this test guards.
	//
	// We intentionally do NOT call r.Stop() inside the storm window. A
	// late RestartApp during STOPPING would set desiredNext=READY_TO_PULL,
	// which routes the app away from STOPPED on T21 and stalls the
	// stop-continuation (allAppsTerminalInGroup never becomes true). The
	// deferred Shutdown drains r.Stop() after workers have finished.
	const iters = 10
	for range 4 {
		wg.Go(func() {
			for range iters {
				_ = r.RestartApp("app-a")
				_ = r.RestartApp("app-b")
				restartCalls.Add(2)
				time.Sleep(time.Millisecond)
			}
		})
	}

	wg.Wait()

	// Assert the stressor actually executed — not a smoke check of atomicity,
	// just a guard against the test becoming a no-op if future refactors
	// short-circuit RestartApp early.
	assert.Positive(t, restartCalls.Load(),
		"test loop did not execute any RestartApp calls")
}
