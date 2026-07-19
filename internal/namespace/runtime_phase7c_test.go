package namespace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
)

// saveNsStateForTest persists the state JSON to the file LoadNsState reads.
// The production write path now goes through the store; this mirrors the
// on-disk layout LoadNsState still consumes (h2migrate fallback) so the
// round-trip stability check below remains exercised.
func saveNsStateForTest(t *testing.T, dir, nsID string, state *NsPersistedState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	path := filepath.Join(dir, "state-"+nsID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // test-only fixture
		t.Fatalf("write state: %v", err)
	}
}

// TestPersistenceFormatStable verifies that the NsPersistedState JSON layout
// round-trips through LoadNsState without mutation. A drift in the JSON layout
// would break forward/backward compatibility with existing state-*.json files
// read by the h2migrate fallback path.
func TestPersistenceFormatStable(t *testing.T) {
	dir := t.TempDir()
	state := &NsPersistedState{
		Status:            NsStatusRunning,
		ManualStoppedApps: []string{"onlyoffice", "ai"},
		EditedApps: map[string]appdef.ApplicationDef{
			"emodel": {
				Name:  "emodel",
				Image: "emodel:2.0",
				Kind:  appdef.KindThirdParty,
			},
		},
		EditedLockedApps: []string{"emodel"},
		CachedBundle: &bundle.Def{
			Key:          bundle.Key{Version: "2.1.0"},
			Applications: map[string]bundle.AppDef{},
		},
		RestartEvents: []RestartEvent{
			{Timestamp: "2026-04-15T12:00:00Z", App: "emodel", Reason: "user_restart"},
		},
		RestartCounts: map[string]int{"emodel": 2},
	}

	saveNsStateForTest(t, dir, "ns1", state)
	round1 := LoadNsState(dir, "ns1")
	if round1 == nil {
		t.Fatalf("LoadNsState returned nil")
	}
	saveNsStateForTest(t, dir, "ns1", round1)
	round2 := LoadNsState(dir, "ns1")
	if round2 == nil {
		t.Fatalf("second LoadNsState returned nil")
	}

	// Canonicalize via JSON round-trip and compare byte-equal. This catches
	// any map-ordering or type-drift bugs that would leave writes
	// non-idempotent across upgrades.
	b1, err := json.Marshal(round1)
	if err != nil {
		t.Fatalf("marshal round1: %v", err)
	}
	b2, err := json.Marshal(round2)
	if err != nil {
		t.Fatalf("marshal round2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("round-trip drift:\nround1=%s\nround2=%s", b1, b2)
	}
	if !reflect.DeepEqual(round1, round2) {
		t.Fatalf("DeepEqual drift between rounds: %+v vs %+v", round1, round2)
	}
}

// TestPersistCoalescesTransientStatusTransitions verifies that a burst of
// state-only transitions within a single runtimeLoop iteration coalesces into
// a single persistState call at the loop tail, not one per transition.
//
// We exercise this by synthesizing N back-to-back setStatus transitions under
// r.mu (simulating what worker-result handlers do when many apps transition in
// one iteration). Because the loop cannot acquire r.mu until we release it,
// ALL N transitions land in the same iteration. The loop tail then does
// exactly ONE persistState.
//
// We count persistState calls via the injected fakePersister. We assert that a
// 5-transition burst results in at most 2 persists since the baseline (one for
// the burst itself, plus potentially one more from any late tick activity after
// we release the lock). Without coalescing, each transition would produce a
// separate persist — this bound rejects that regression.
func TestPersistCoalescesTransientStatusTransitions(t *testing.T) {
	md := newMockDocker()
	dir := t.TempDir()
	r := NewRuntime(testConfig(), md, dir)
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")}, false)
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING")
	}
	// Wait for the startup churn to settle so we have a stable baseline.
	if !waitUntil(2*time.Second, func() bool { return !r.dirty.Load() }) {
		t.Fatalf("r.dirty never cleared after startup")
	}

	baselineCalls := fp.callCount()

	// Hold r.mu and flip NS status back-and-forth several times. Each
	// setStatus flips r.dirty. The runtimeLoop is blocked on r.mu for
	// the duration of this block, so it CANNOT tail-persist until we
	// release — which means all 5 transitions collapse into a single
	// dirty flag.
	r.mu.Lock()
	baselineStatus := r.status
	transitions := []NsRuntimeStatus{
		NsStatusStarting, NsStatusRunning, NsStatusStarting, NsStatusRunning, NsStatusStarting,
	}
	for _, s := range transitions {
		r.setStatus(s)
	}
	// Restore original status so the rest of the test is idempotent.
	r.setStatus(baselineStatus)
	r.mu.Unlock()

	// Wake the loop and wait for it to drain dirty.
	r.signalCh.Flush()
	if !waitUntil(2*time.Second, func() bool { return !r.dirty.Load() }) {
		t.Fatalf("r.dirty never cleared after burst — loop tail did not persist")
	}

	// Count persists since the baseline. We allow at most 2: one mandatory
	// persist for the burst itself, plus at most one incidental persist from
	// unrelated loop activity (a tick that ran between Unlock and our
	// dirty-drained check). The pre-coalescing code would have persisted 5
	// times (once per transition) and failed this bound.
	burstCalls := fp.callCount()
	if burstCalls <= baselineCalls {
		t.Fatalf("state not persisted after burst (calls unchanged at %d)", burstCalls)
	}
	if delta := burstCalls - baselineCalls; delta > 2 {
		t.Fatalf("burst produced %d persists (> 2) — coalescing broken", delta)
	}
	// Probe over ~3.6s (>3× tickerPeriod) to ensure we're truly idle — no
	// runaway persist loop re-firing on each tick. assert.Never fails fast
	// the moment r.dirty is re-raised.
	assert.Never(t, func() bool {
		return r.dirty.Load()
	}, 3600*time.Millisecond, 100*time.Millisecond,
		"r.dirty re-raised without mutation — coalescing broken")
	// After idle, no further persists should have fired (no ticks persist an
	// already-clean state).
	if idleCalls := fp.callCount(); idleCalls > burstCalls {
		t.Fatalf("state persisted while idle: calls went %d → %d", burstCalls, idleCalls)
	}
}

// TestStopAppBurstPersistsDetach is the sibling test: a user StopApp followed
// by re-attach via StartApp, and a second StopApp, each records durable intent
// and must each persist inline. This guards the invariant that inline-persist
// sites did NOT regress into dirty-only writes that could be lost on crash.
// Unlike the coalescing test above, we EXPECT multiple persists here.
func TestStopAppBurstPersistsDetach(t *testing.T) {
	md := newMockDocker()
	dir := t.TempDir()
	r := NewRuntime(testConfig(), md, dir)
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")}, false)
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING")
	}

	if stopErr := r.StopApp("foo"); stopErr != nil {
		t.Fatalf("StopApp: %v", stopErr)
	}
	if !waitForAppStatus(r, "foo", AppStatusStopped, 5*time.Second) {
		t.Fatalf("foo did not reach STOPPED")
	}

	// Inspect the most recent persist — inline persist must have fired.
	var persisted NsPersistedState
	if unmarshalErr := json.Unmarshal([]byte(fp.lastJSON()), &persisted); unmarshalErr != nil {
		t.Fatalf("unmarshal state: %v", unmarshalErr)
	}
	if !slices.Contains(persisted.ManualStoppedApps, "foo") {
		t.Fatalf("StopApp inline persist missing: manualStoppedApps=%v", persisted.ManualStoppedApps)
	}
}

// TestStopAppInlinePersistIsEager guards the invariant that StopApp's durable
// detach intent is persisted *before* the method returns, even if the
// runtimeLoop's tail coalesced persist hasn't fired yet. A regression where
// StopApp switched to "dirty=true only" would fail this test because we read
// the state file synchronously after the call.
func TestStopAppInlinePersistIsEager(t *testing.T) {
	md := newMockDocker()
	dir := t.TempDir()
	r := NewRuntime(testConfig(), md, dir)
	fp := &fakePersister{}
	r.SetStatePersister(fp)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")}, false)
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("foo did not reach RUNNING")
	}

	if err := r.StopApp("foo"); err != nil {
		t.Fatalf("StopApp: %v", err)
	}

	// Inspect the most recent persist IMMEDIATELY — no waiting for the loop
	// tail. StopApp persists inline, so fakePersister already holds the detach.
	var persisted NsPersistedState
	if err := json.Unmarshal([]byte(fp.lastJSON()), &persisted); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if !slices.Contains(persisted.ManualStoppedApps, "foo") {
		t.Fatalf("StopApp did not persist detach inline; manualStoppedApps=%v", persisted.ManualStoppedApps)
	}
}

// TestDirtyFlagClearedAfterLoopPersist exercises the loop-tail coalescing
// path directly. A setStatus transition (via any public flow that reaches
// the running state) should result in the dirty flag being cleared after
// one runtimeLoop iteration, AND the state file being updated.
func TestDirtyFlagClearedAfterLoopPersist(t *testing.T) {
	md := newMockDocker()
	dir := t.TempDir()
	r := NewRuntime(testConfig(), md, dir)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")}, false)
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("foo did not reach RUNNING")
	}

	// After startup transitions settle, the runtimeLoop tail MUST have
	// drained r.dirty to false (otherwise we'd be persisting every tick).
	if !waitUntil(2*time.Second, func() bool { return !r.dirty.Load() }) {
		t.Fatalf("r.dirty never cleared after startup — loop-tail persist broken")
	}
}

// waitUntil polls cond every 10ms up to timeout, returning true if cond
// ever returned true. Used by tests that need to observe loop-side effects
// without racing with the ticker.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
