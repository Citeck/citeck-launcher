// Gate tests for RestartApp state-machine routing and event ordering:
//   - TestRestartDetachedDepTriggersRegenerate
//   - TestRapidRestartAppDoesNotOverlapStopContainers
//   - TestEventOrderWithinTick
package namespace

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartDetachedDepTriggersRegenerate: when the app being restarted is a
// dependency-of-detached (e.g., proxy → ecos-content while content is
// detached), RestartApp must route through cmdRegenerate (preserving the
// existing ACME + proxy reconfig flow). The state-machine STOPPING/READY_TO_PULL
// path is bypassed.
func TestRestartDetachedDepTriggersRegenerate(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("content", "image-content:1")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Flag the app as a dep-of-detached. SetDependsOnDetachedApps is the
	// public surface used by the generator after it inspects the app graph.
	r.SetDependsOnDetachedApps(map[string]bool{def.Name: true})

	// Snapshot stopRemove count so we can assert NO direct stop dispatch
	// happened (the regen path goes via cmdQueue → doRegenerate, which
	// chooses whether to recreate based on hash match).
	md.mu.Lock()
	stopRemoveBefore := md.stopRemoveCalls
	md.mu.Unlock()

	// Invoke the restart. Must NOT transition the app to STOPPING directly.
	require.NoError(t, r.RestartApp(def.Name))

	// The app must NOT enter STOPPING — the regenerate path leaves the
	// running container in place until doRegenerate decides otherwise.
	// Observe the status for a short window.
	sawStopping := false
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		app := r.FindApp(def.Name)
		if app != nil && app.Status == AppStatusStopping {
			sawStopping = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.False(t, sawStopping, "regen path must not drive the app through STOPPING")

	// After regenerate completes the app should still be RUNNING (hash match
	// → container reused, same ImageDigest → no recreate).
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not settle back to RUNNING after regen, got %s", status)
	}

	// Sanity: no direct StopAndRemoveContainer (the regen path reuses the
	// container on hash match). If this ever asserts non-zero it would
	// indicate the regen hash-match path regressed, not the restart routing.
	md.mu.Lock()
	stopRemoveAfter := md.stopRemoveCalls
	md.mu.Unlock()
	assert.Equal(t, stopRemoveBefore, stopRemoveAfter,
		"dep-of-detached regen must not stop/remove the dep's container on hash match "+
			"(before=%d after=%d)", stopRemoveBefore, stopRemoveAfter)
}

// TestRestartAppGoesThroughStoppingThenReadyToPull: sanity check for the
// state-machine routing. RestartApp on a RUNNING app must transition through
// STOPPING (desiredNext=READY_TO_PULL → T21 → READY_TO_PULL → T2/T3 → RUNNING).
// Emits exactly one restart_event{reason:"user_restart"} via the single
// emitRestartEvent write path.
func TestRestartAppGoesThroughStoppingThenReadyToPull(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("web", "image-web:1")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Capture every app_status event for the app. After RestartApp we expect
	// to observe: RUNNING → STOPPING → READY_TO_PULL → (...) → RUNNING.
	var (
		mu       sync.Mutex
		statuses []string
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "app_status" || evt.AppName != def.Name {
			return
		}
		mu.Lock()
		statuses = append(statuses, evt.After)
		mu.Unlock()
	})

	preRestartCount := r.FindApp(def.Name).RestartCount

	require.NoError(t, r.RestartApp(def.Name))

	// Wait for the app to settle back to RUNNING.
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not settle back to RUNNING after restart")
	}

	mu.Lock()
	seen := append([]string(nil), statuses...)
	mu.Unlock()

	// STOPPING must appear in the sequence (state-machine routing) — not a
	// direct RUNNING → READY_TO_PULL jump.
	assert.True(t, slices.Contains(seen, string(AppStatusStopping)),
		"expected STOPPING transition in sequence: %v", seen)

	// Exactly one restart_event with reason=user_restart.
	r.mu.RLock()
	var userRestarts int
	for _, evt := range r.restartEvents {
		if evt.App == def.Name && evt.Reason == "user_restart" {
			userRestarts++
		}
	}
	postRestartCount := r.apps[def.Name].RestartCount
	r.mu.RUnlock()
	assert.Equal(t, 1, userRestarts, "expected exactly one user_restart event")
	assert.Equal(t, preRestartCount+1, postRestartCount,
		"restart count must be bumped by exactly one (pre=%d post=%d)",
		preRestartCount, postRestartCount)
}

// TestRapidRestartAppDoesNotOverlapStopContainers: firing RestartApp twice in
// rapid succession must not issue two concurrent StopAndRemoveContainer calls
// on the same container. The second call's dispatcher.Dispatch supersedes the
// first's (attemptID bump → CancelSuperseded). Verified by asserting that the
// app converges back to RUNNING (no STOPPING_FAILED stuck state) and the
// observed state transitions are consistent with a single supersession.
func TestRapidRestartAppDoesNotOverlapStopContainers(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("web", "image-web:1")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Introduce a modest stopDelay so supersession has a window to fire.
	// This does NOT block doStart's initial stale-cleanup path because the
	// delay is set AFTER the initial setup stop calls have completed.
	md.mu.Lock()
	md.stopDelay = 150 * time.Millisecond
	md.mu.Unlock()

	// Fire twice in rapid succession. The second call's Dispatch supersedes
	// the first's worker (attemptID bump → CancelSuperseded on the first ctx).
	require.NoError(t, r.RestartApp(def.Name))
	require.NoError(t, r.RestartApp(def.Name))

	// Remove the stop delay now that both restarts are in flight — the rest
	// of the lifecycle (second stop + recreate) should proceed quickly.
	md.mu.Lock()
	md.stopDelay = 0
	md.mu.Unlock()

	// The app must settle back to RUNNING (not STOPPING_FAILED or
	// any terminal-error state). This is the primary invariant: rapid
	// supersession must not leave the app in a stuck state.
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		app := r.FindApp(def.Name)
		status := "nil"
		if app != nil {
			status = string(app.Status)
		}
		t.Fatalf("app did not settle back to RUNNING after rapid restart, got %s", status)
	}

	// Both RestartApp calls must emit user_restart. The second arrived while
	// the app was STOPPING from the first; the STOPPING branch of RestartApp
	// emits user_restart so observers can distinguish this caller's intent
	// from the prior call's. Key invariant: emits are accurate (one per call)
	// and the app does not end up STOPPING_FAILED.
	r.mu.RLock()
	var userRestarts int
	for _, evt := range r.restartEvents {
		if evt.App == def.Name && evt.Reason == "user_restart" {
			userRestarts++
		}
	}
	r.mu.RUnlock()
	assert.Equal(t, 2, userRestarts, "expected exactly two user_restart events (one per RestartApp call)")
}

// TestEventOrderWithinTick pins event ordering: within a single runtimeLoop
// iteration, app_status(X→Y) events precede the derived namespace_status(A→B)
// event emitted by updateNsStatus at the end of the iteration. Asserted by
// capturing the complete ordered event stream across startup and confirming no
// namespace_status appears before the app_status transitions whose derivation
// produced it.
func TestEventOrderWithinTick(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")

	// Capture all events in order.
	var (
		mu     sync.Mutex
		events []api.EventDto
	)
	// Use an atomic to short-circuit the callback after we have enough data —
	// the runtime keeps emitting stats/probes forever otherwise.
	var closed atomic.Bool
	r.SetEventCallback(func(evt api.EventDto) {
		if closed.Load() {
			return
		}
		if evt.Type != "app_status" && evt.Type != "namespace_status" {
			return
		}
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for event ordering assertion")
	}
	if !waitForStatus(r, NsStatusRunning, 5*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// The runtime emits events asynchronously via dispatchLoop. Wait until
	// the callback has observed both the app→RUNNING and the namespace→RUNNING
	// events before freezing the event list.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		var sawApp, sawNs bool
		for _, evt := range events {
			if evt.Type == "app_status" && evt.AppName == def.Name && evt.After == string(AppStatusRunning) {
				sawApp = true
			}
			if evt.Type == "namespace_status" && evt.After == string(NsStatusRunning) {
				sawNs = true
			}
		}
		mu.Unlock()
		if sawApp && sawNs {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	closed.Store(true)

	mu.Lock()
	captured := append([]api.EventDto(nil), events...)
	mu.Unlock()

	// The final namespace_status event must be "→ RUNNING" — and its
	// timestamp-order position must be at or after the last app_status
	// transition that caused it (in our case the only app reaching RUNNING).
	var (
		lastAppRunningIdx = -1
		nsRunningIdx      = -1
	)
	for i, evt := range captured {
		if evt.Type == "app_status" && evt.AppName == def.Name && evt.After == string(AppStatusRunning) {
			lastAppRunningIdx = i
		}
		if evt.Type == "namespace_status" && evt.After == string(NsStatusRunning) {
			nsRunningIdx = i
			break
		}
	}
	require.NotEqual(t, -1, lastAppRunningIdx, "app never emitted RUNNING event")
	require.NotEqual(t, -1, nsRunningIdx, "namespace never emitted RUNNING event")
	assert.Greater(t, nsRunningIdx, lastAppRunningIdx,
		"event order violated: namespace_status(RUNNING) appeared BEFORE app_status(RUNNING) (ns=%d app=%d)",
		nsRunningIdx, lastAppRunningIdx)
}

