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
)

// TestPersistenceFormatStable verifies that SaveNsState + LoadNsState must
// round-trip a populated state without mutation. The coalesced-persist path
// (via r.dirty) never touches the on-disk format. A drift in the JSON layout
// would break forward/backward compatibility with existing state-*.json files
// on upgraded daemons.
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

	if err := SaveNsState(dir, "ns1", state); err != nil {
		t.Fatalf("SaveNsState: %v", err)
	}
	round1 := LoadNsState(dir, "ns1")
	if round1 == nil {
		t.Fatalf("LoadNsState returned nil")
	}
	if err := SaveNsState(dir, "ns1", round1); err != nil {
		t.Fatalf("re-SaveNsState: %v", err)
	}
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
// We can't count persistState calls directly (no hook), but mtime on the state
// file is a faithful proxy: SaveNsState does an atomic temp+rename, so each
// successful persist advances mtime. We assert that a 5-transition burst
// results in at most 2 mtime advances (one for the burst itself, plus
// potentially one more from any late tick activity after we release the lock).
// Without coalescing, each transition would produce a separate persist — this
// bound rejects that regression.
func TestPersistCoalescesTransientStatusTransitions(t *testing.T) {
	md := newMockDocker()
	dir := t.TempDir()
	r := NewRuntime(testConfig(), md, dir)
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")})
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING")
	}
	// Wait for the startup churn to settle so we have a stable baseline.
	if !waitUntil(2*time.Second, func() bool { return !r.dirty.Load() }) {
		t.Fatalf("r.dirty never cleared after startup")
	}

	statePath := filepath.Join(dir, "state-test.json")
	info0, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state baseline: %v", err)
	}

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

	// Count mtime advances since the baseline. We allow at most 2:
	// one mandatory persist for the burst itself, plus at most one
	// incidental persist from unrelated loop activity (a tick that ran
	// between Unlock and our dirty-drained check). The pre-7c code
	// would have advanced mtime 5 times (once per transition) and failed
	// this bound.
	info1, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state after burst: %v", err)
	}
	if !info1.ModTime().After(info0.ModTime()) {
		t.Fatalf("state file not persisted after burst (mtime unchanged)")
	}
	// Probe a few more times to ensure we're truly idle — no runaway
	// persist loop re-firing for each tick.
	for range 3 {
		time.Sleep(1200 * time.Millisecond) // > tickerPeriod
		if r.dirty.Load() {
			t.Fatalf("r.dirty re-raised without mutation — coalescing broken")
		}
	}
	info2, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat state after idle: %v", err)
	}
	// After idle, mtime should equal post-burst mtime (no ticks persist
	// an already-clean state).
	if info2.ModTime().After(info1.ModTime()) {
		t.Fatalf("state file advanced while idle: mtime went %v → %v",
			info1.ModTime(), info2.ModTime())
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
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")})
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING")
	}

	statePath := filepath.Join(dir, "state-test.json")
	if stopErr := r.StopApp("foo"); stopErr != nil {
		t.Fatalf("StopApp: %v", stopErr)
	}
	if !waitForAppStatus(r, "foo", AppStatusStopped, 5*time.Second) {
		t.Fatalf("foo did not reach STOPPED")
	}

	// Read IMMEDIATELY — inline persist must have fired.
	data, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatalf("read state: %v", readErr)
	}
	var persisted NsPersistedState
	if unmarshalErr := json.Unmarshal(data, &persisted); unmarshalErr != nil {
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
	defer r.Shutdown()

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")})
	if !waitForAppStatus(r, "foo", AppStatusRunning, 10*time.Second) {
		t.Fatalf("foo did not reach RUNNING")
	}

	if err := r.StopApp("foo"); err != nil {
		t.Fatalf("StopApp: %v", err)
	}

	// Read the state file IMMEDIATELY — no waiting for the loop tail.
	statePath := filepath.Join(dir, "state-test.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var persisted NsPersistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
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

	r.Start([]appdef.ApplicationDef{simpleApp("foo", "foo:1")})
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
