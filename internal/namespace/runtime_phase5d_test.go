// Behavioral tests for T31: STOPPING_FAILED self-heal. A runtime-driven stop
// (liveness recreate / reload sweep) that times out lands in STOPPING_FAILED;
// T31 re-dispatches the stop and recreates the app after the same exponential
// backoff as T24/T25. Operator detaches (manualStoppedApps) are never touched.
package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
)

// TestStoppingFailedSelfHeal exercises T31: a non-detached app in
// STOPPING_FAILED must NOT recreate during the backoff window, then must
// self-heal (recreate → RUNNING) once the window elapses.
func TestStoppingFailedSelfHeal(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Force STOPPING_FAILED with a recent retry attempt (count=1, backoff=1m).
	// Not detached — so the stepAllApps loop does not skip it.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.setAppStatus(app, AppStatusStoppingFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now()},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	// Within the backoff window — T31 must not fire.
	assert.Never(t, func() bool {
		return r.FindApp(def.Name).Status != AppStatusStoppingFailed
	}, 1500*time.Millisecond, 50*time.Millisecond,
		"T31 fired prematurely: left STOPPING_FAILED during backoff window")

	// Move lastAttempt past the window — T31 fires, app recreates and returns
	// to RUNNING (mock stop/pull/start are non-blocking).
	r.mu.Lock()
	r.retryState[def.Name] = retryInfo{count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)}
	r.mu.Unlock()
	r.signalCh.Flush()

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("T31 did not self-heal: app stuck at %s", r.FindApp(def.Name).Status)
	}
}

// TestStoppingFailedDetachedNotHealed pins that a DETACHED app
// (manualStoppedApps) in STOPPING_FAILED is never recreated by T31 — operator
// intent to stop wins, even with the backoff window long elapsed.
func TestStoppingFailedDetachedNotHealed(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Detached + STOPPING_FAILED, backoff long elapsed. T31 must still skip it.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.manualStoppedApps[def.Name] = true
	r.setAppStatus(app, AppStatusStoppingFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	assert.Never(t, func() bool {
		return r.FindApp(def.Name).Status != AppStatusStoppingFailed
	}, 1500*time.Millisecond, 50*time.Millisecond,
		"T31 recreated a detached app — operator stop intent must win")
}
