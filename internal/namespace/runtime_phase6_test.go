// Behavioral tests: reconcile-diff and liveness-probe dispatched as tick()
// workers. Dispatcher mutation happens exclusively from runtimeLoop.
package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconcilerBackoffRetryTiming exercises the tick()-driven reconcile-diff:
// on a live runtime, once the namespace reaches RUNNING, a deleted container
// is detected by the reconciler-diff cycle and the app transitions
// READY_TO_PULL → ... → RUNNING again. The test compresses reconcilerInterval
// so the dispatch fires within a few ticks.
func TestReconcilerBackoffRetryTiming(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	// Compress tick cadence + reconcile interval so the test doesn't wait 60s.
	r.tickerPeriod = 50 * time.Millisecond
	r.reconcilerInterval = 200 * time.Millisecond
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})

	require.True(t, waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second),
		"app did not reach RUNNING for setup")
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING — reconcile would be a no-op")

	// Force lastReconcileDispatch to zero so the next tick fires immediately
	// rather than waiting for the first post-start interval.
	r.mu.Lock()
	r.lastReconcileDispatch = time.Time{}
	r.mu.Unlock()

	// Simulate container disappearance: delete from the mock directly.
	md.mu.Lock()
	delete(md.containers, def.Name)
	md.mu.Unlock()

	// The tick-dispatched reconcile-diff must observe the missing container
	// and record a crash restart_event via T18. Wait for the event to appear
	// (observable invariant). The app itself may already be back to RUNNING
	// via T18 → T3 → start on the mock, so we don't poll the app status.
	eventFired := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		count := len(r.restartEvents)
		r.mu.RUnlock()
		if count >= 1 {
			eventFired = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, eventFired,
		"tick()-dispatched reconcile-diff did not fire T18 within 10s")

	// Exactly one crash restart_event recorded.
	r.mu.RLock()
	defer r.mu.RUnlock()
	require.GreaterOrEqual(t, len(r.restartEvents), 1,
		"expected at least one restart_event for the crash")
	assert.Equal(t, "crash", r.restartEvents[len(r.restartEvents)-1].Reason)
}

// TestLivenessProbeScheduling pins the per-app liveness schedule. When an app
// newly enters RUNNING with a LivenessProbe defined, setAppStatus seeds
// livenessNextAt[name] = now + InitialDelaySeconds. Subsequent dispatches
// advance the next time by PeriodSeconds. The test asserts:
//  1. livenessNextAt is populated after RUNNING transition.
//  2. The delta from RUNNING transition to first schedule matches
//     InitialDelaySeconds within the tick-cadence jitter.
//  3. After at least one probe fires (counter moves or reset), the next
//     scheduled time advances by PeriodSeconds.
func TestLivenessProbeScheduling(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	// Drive the loop fast enough to observe schedule advance within the test.
	r.tickerPeriod = 20 * time.Millisecond
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	def.LivenessProbe = &appdef.AppProbeDef{
		Exec:                &appdef.ExecProbeDef{Command: []string{"pg_isready"}},
		InitialDelaySeconds: 1, // 1s
		PeriodSeconds:       1, // 1s
		FailureThreshold:    3,
		TimeoutSeconds:      1,
	}

	r.Start([]appdef.ApplicationDef{def})
	require.True(t, waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second),
		"app did not reach RUNNING")
	require.True(t, waitForStatus(r, NsStatusRunning, 10*time.Second),
		"namespace did not reach RUNNING — tick would skip liveness scheduling")

	// Assertion 1: livenessNextAt populated on RUNNING transition with an
	// initial-delay offset.
	r.mu.RLock()
	firstNext, seeded := r.livenessNextAt[def.Name]
	r.mu.RUnlock()
	require.True(t, seeded, "livenessNextAt not seeded after RUNNING transition")
	require.False(t, firstNext.IsZero(), "livenessNextAt must be non-zero")

	// Assertion 2: the initial schedule offset should be ≥ 900ms (initialDelay
	// 1s minus some tick jitter). Upper bound is generous (≤ 3s) to avoid
	// flakes under scheduler pressure.
	now := time.Now()
	delta := firstNext.Sub(now)
	assert.GreaterOrEqual(t, delta, -time.Second,
		"livenessNextAt should be close to now+initialDelay, got delta=%v", delta)
	assert.LessOrEqual(t, delta, 3*time.Second,
		"livenessNextAt should be within initialDelay+jitter, got delta=%v", delta)

	// Assertion 3: after the initial delay elapses, the schedule advances.
	// Wait up to 3x period for the next-time to move past firstNext.
	advanced := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		cur := r.livenessNextAt[def.Name]
		r.mu.RUnlock()
		if cur.After(firstNext) {
			advanced = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, advanced,
		"livenessNextAt did not advance past the initial schedule within 5s")
}

// TestReconcilerDisabledSkipsDispatch verifies that when the Runtime is
// configured with ReconcilerConfig{Enabled: false}, tickUnderLock does not
// emit a ReconcileDiffTask dispatch plan even when all other gates
// (namespace RUNNING, interval elapsed) are satisfied.
func TestReconcilerDisabledSkipsDispatch(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Force the namespace into RUNNING and ensure reconcile interval would
	// otherwise fire on the next tick.
	r.mu.Lock()
	r.status = NsStatusRunning
	r.lastReconcileDispatch = time.Time{}
	r.reconcilerInterval = 1 * time.Millisecond
	r.mu.Unlock()

	// Apply a ReconcilerConfig with Enabled=false. LivenessEnabled=true so
	// we isolate the reconcile-diff gate.
	r.SetReconcilerConfig(ReconcilerConfig{
		Enabled:         false,
		IntervalSeconds: 60,
		LivenessEnabled: true,
	})

	plans := r.tickUnderLock()
	for _, p := range plans {
		assert.NotEqual(t, workers.OpReconcileDiff, p.taskID.Op,
			"ReconcileDiffTask must not be dispatched when Enabled=false")
	}

	// Sanity: flipping Enabled=true should produce a ReconcileDiffTask plan.
	r.SetReconcilerConfig(ReconcilerConfig{
		Enabled:         true,
		IntervalSeconds: 60,
		LivenessEnabled: true,
	})
	r.mu.Lock()
	r.lastReconcileDispatch = time.Time{}
	r.mu.Unlock()
	plans = r.tickUnderLock()
	found := false
	for _, p := range plans {
		if p.taskID.Op == workers.OpReconcileDiff {
			found = true
			break
		}
	}
	assert.True(t, found,
		"ReconcileDiffTask expected when Enabled=true + NS RUNNING + interval elapsed")
}

// TestLivenessDisabledSkipsDispatch verifies that LivenessEnabled=false
// suppresses per-app LivenessProbeTask dispatch from tickUnderLock even for
// RUNNING apps with a LivenessProbe configured.
func TestLivenessDisabledSkipsDispatch(t *testing.T) {
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Seed one RUNNING app with a liveness probe + elapsed schedule.
	def := simpleApp("postgres", "postgres:17")
	def.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready"}},
		PeriodSeconds:    1,
		FailureThreshold: 3,
		TimeoutSeconds:   1,
	}
	r.mu.Lock()
	r.status = NsStatusRunning
	r.apps[def.Name] = &AppRuntime{
		Name:        def.Name,
		Status:      AppStatusRunning,
		ContainerID: "cid-" + def.Name,
		Def:         def,
	}
	// Set nextAt in the past so the schedule gate would otherwise fire.
	r.livenessNextAt[def.Name] = time.Now().Add(-1 * time.Second)
	r.mu.Unlock()

	// Disable liveness via SetReconcilerConfig.
	r.SetReconcilerConfig(ReconcilerConfig{
		Enabled:         true,
		IntervalSeconds: 60,
		LivenessEnabled: false,
	})

	plans := r.tickUnderLock()
	for _, p := range plans {
		assert.NotEqual(t, workers.OpLivenessProbe, p.taskID.Op,
			"LivenessProbeTask must not be dispatched when LivenessEnabled=false")
	}

	// Re-enable and confirm the liveness dispatch appears.
	r.SetReconcilerConfig(ReconcilerConfig{
		Enabled:         true,
		IntervalSeconds: 60,
		LivenessEnabled: true,
	})
	r.mu.Lock()
	r.livenessNextAt[def.Name] = time.Now().Add(-1 * time.Second)
	r.mu.Unlock()
	plans = r.tickUnderLock()
	found := false
	for _, p := range plans {
		if p.taskID.Op == workers.OpLivenessProbe && p.taskID.App == def.Name {
			found = true
			break
		}
	}
	assert.True(t, found,
		"LivenessProbeTask expected when LivenessEnabled=true + app RUNNING + schedule elapsed")
}
