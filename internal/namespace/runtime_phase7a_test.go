// Tests for the typed command queue: coalescing and back-pressure.
//   - TestCmdQueueCoalesces — table-driven check of coalesce pairs (the
//     cmdqueue_test.go covers base pairs; this file adds cross-app variants
//     and a matrix to catch regressions).
//   - TestCmdQueueBackpressure — Enqueue returns ErrCmdQueueFull on a full
//     buffer within the 500 ms timeout window.
package namespace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCmdQueueCoalesces runs every coalesce row through
// collapseCommandsIfPossible directly (no queue buffering), plus a handful of
// negative pairs that must stay separate.
func TestCmdQueueCoalesces(t *testing.T) {
	type outcome int
	const (
		takeB  outcome = iota // coalesce to b
		takeA                 // coalesce to a (cmdStart absorbs trailing cmdRegenerate)
		noJoin                // don't coalesce
	)
	cases := []struct {
		name string
		a, b runtimeCmd
		want outcome
	}{
		// NS-wide.
		{"Start+Start", cmdStart{}, cmdStart{}, takeB},
		{"Start+Stop", cmdStart{}, cmdStop{}, takeB},
		{"Stop+Start", cmdStop{}, cmdStart{}, takeB},
		{"Stop+Stop", cmdStop{}, cmdStop{}, takeB},
		{"Regen+Start", cmdRegenerate{}, cmdStart{}, takeB},
		{"Start+Regen", cmdStart{}, cmdRegenerate{}, takeA},
		{"Regen+Regen", cmdRegenerate{}, cmdRegenerate{}, takeB},

		// Per-app.
		{"StopAppX+StopAppX", cmdStopApp{name: "x"}, cmdStopApp{name: "x"}, takeB},
		{"StopAppX+StartAppX", cmdStopApp{name: "x"}, cmdStartApp{name: "x"}, takeB},
		{"StartAppX+StopAppX", cmdStartApp{name: "x"}, cmdStopApp{name: "x"}, takeB},
		{"RestartAppX+RestartAppX", cmdRestartApp{name: "x"}, cmdRestartApp{name: "x"}, takeB},

		// Retry.
		{"RetryPull+RetryPull", cmdRetryPullFailed{}, cmdRetryPullFailed{}, takeB},

		// Negative — must NOT coalesce.
		{"StopAppX+StopAppY", cmdStopApp{name: "x"}, cmdStopApp{name: "y"}, noJoin},
		{"StopAppX+StartAppY", cmdStopApp{name: "x"}, cmdStartApp{name: "y"}, noJoin},
		{"StartAppX+StopAppY", cmdStartApp{name: "x"}, cmdStopApp{name: "y"}, noJoin},
		{"RestartAppX+RestartAppY", cmdRestartApp{name: "x"}, cmdRestartApp{name: "y"}, noJoin},
		{"Stop+Regen", cmdStop{}, cmdRegenerate{}, noJoin},
		{"Regen+Stop", cmdRegenerate{}, cmdStop{}, noJoin},
		{"StopApp+Start", cmdStopApp{name: "x"}, cmdStart{}, noJoin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, ok := collapseCommandsIfPossible(tc.a, tc.b)
			switch tc.want {
			case takeB:
				assert.True(t, ok, "expected coalesce")
				assert.Equal(t, tc.b, merged)
			case takeA:
				assert.True(t, ok, "expected coalesce (cmd0 wins)")
				assert.Equal(t, tc.a, merged)
			case noJoin:
				assert.False(t, ok, "expected no coalesce")
			}
		})
	}
}

// TestCmdQueueBackpressure verifies that Enqueue returns ErrCmdQueueFull
// after the cmdQueueEnqueueTimeout window expires on a full buffer.
// Callers translate this to HTTP 503.
func TestCmdQueueBackpressure(t *testing.T) {
	q := NewCmdQueue()
	for range cmdQueueCapacity {
		require.NoError(t, q.Enqueue(cmdRetryPullFailed{}))
	}
	// One more enqueue must time out; budget a hair over the 500 ms threshold.
	start := time.Now()
	err := q.Enqueue(cmdRetryPullFailed{})
	elapsed := time.Since(start)
	require.ErrorIs(t, err, ErrCmdQueueFull)
	// The enqueue must wait approximately cmdQueueEnqueueTimeout — confirming
	// it didn't return ErrCmdQueueFull prematurely (immediate-fail would be a
	// regression that breaks legitimate short bursts).
	assert.GreaterOrEqual(t, elapsed, cmdQueueEnqueueTimeout-50*time.Millisecond,
		"enqueue should wait at least cmdQueueEnqueueTimeout before failing")
	assert.Less(t, elapsed, cmdQueueEnqueueTimeout+500*time.Millisecond,
		"enqueue should not wait significantly longer than cmdQueueEnqueueTimeout")
}

// TestDetachSkipsPostDetachStepDispatches pins the post-detach tail-iteration
// race: after cmdDetach's doDetach runs, the current runtimeLoop iteration
// still executes its tail (stepAllApps / tick / …) before the next select
// observes shutdownComplete and exits. Without the `detaching` guard,
// pre-RUNNING apps (READY_TO_PULL / DEPS_WAITING / START_FAILED / PULL_FAILED)
// would emit dispatch plans and launch workers on the dispatcher's Background
// context — surviving detach on a detaching runtime.
//
// Strategy: construct a production-mode runtime, seed an app directly in
// READY_TO_PULL with a pull-needing image (imageExists=false), then set
// r.detaching=true (mimicking doDetach's first action). Invoke
// stepAllAppsUnderLock and tickUnderLock directly and assert neither returns
// any plans. Also verifies that without the flag the same seeded state WOULD
// produce a pull plan — confirming the test catches the bug.
func TestDetachSkipsPostDetachStepDispatches(t *testing.T) {
	md := newMockDocker()
	// Force pull-needing state: T2 dispatches a pull when !ImageExists.
	md.mu.Lock()
	md.imageExists = map[string]bool{"ecos-model:1.0": false}
	md.mu.Unlock()

	r := NewRuntime(testConfig(), md, t.TempDir())

	// Seed an app directly in READY_TO_PULL (pre-RUNNING state). This mirrors
	// what stepAllApps would see if cmdDetach arrived before the state machine
	// had driven this app forward.
	def := simpleApp("emodel", "ecos-model:1.0")
	r.mu.Lock()
	r.apps[def.Name] = &AppRuntime{
		Name:   def.Name,
		Status: AppStatusReadyToPull,
		Def:    def,
	}
	r.mu.Unlock()

	// Sanity: without the detaching guard the state machine WOULD dispatch a
	// pull plan — this is the behavior the guard suppresses. Assert first so
	// the test fails loud if the assumption about stepAllApps ever changes.
	plans := r.stepAllAppsUnderLock()
	require.Len(t, plans, 1, "pre-detach READY_TO_PULL must dispatch a pull plan")

	// Reset the app back to READY_TO_PULL (stepAllAppsUnderLock transitioned it
	// to PULLING above).
	r.mu.Lock()
	r.apps[def.Name].Status = AppStatusReadyToPull
	r.mu.Unlock()

	// Now mimic doDetach: set detaching BEFORE invoking the loop tail helpers.
	r.detaching.Store(true)

	// stepAllAppsUnderLock MUST short-circuit without emitting plans — no new
	// pull worker may be dispatched on a detaching runtime.
	stepPlans := r.stepAllAppsUnderLock()
	assert.Empty(t, stepPlans,
		"stepAllAppsUnderLock must not emit dispatch plans when detaching is set")

	// App status must remain READY_TO_PULL — the state machine did not run.
	r.mu.RLock()
	status := r.apps[def.Name].Status
	r.mu.RUnlock()
	assert.Equal(t, AppStatusReadyToPull, status,
		"state machine must not transition apps when detaching is set")

	// tickUnderLock MUST also short-circuit (stats / reconcile / liveness /
	// STOPPING-budget scans all skipped on a detaching runtime).
	tickPlans := r.tickUnderLock()
	assert.Empty(t, tickPlans,
		"tickUnderLock must not emit dispatch plans when detaching is set")

	// Also assert no pull actually happened via the mock counter (belt-and-
	// suspenders: pullCalls is only incremented when a pull worker runs, so
	// it's a proxy for the survived-detach dispatch.)
	md.mu.Lock()
	pulls := md.pullCalls
	md.mu.Unlock()
	assert.Equal(t, 0, pulls,
		"no pull worker must execute on a detaching runtime")
}
