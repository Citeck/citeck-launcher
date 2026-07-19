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
	r.Start([]appdef.ApplicationDef{def}, false)
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
	r.Start([]appdef.ApplicationDef{def}, false)
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

// TestSelfHealReusesLocalImageNoPull pins that a T31 self-heal recreates from
// the LOCAL image and does NOT pull. Uses a Citeck-kind :snapshot image
// (shouldPullImage==true) so WITHOUT the reuseLocalImage suppression the
// recreate WOULD pull — making the assertion discriminating. Guards against a
// health restart silently swapping a mutable tag to a new version, and against
// the restart failing on a registry/disk outage. Mirrors Kotlin 1.x
// pullIfPresent=false: the pull action runs but short-circuits on a present
// local image (mock ImageExists defaults to true).
func TestSelfHealReusesLocalImageNoPull(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Citeck-core + :snapshot ⇒ shouldPullImage==true ⇒ a normal recreate pulls.
	def := simpleApp("eapps", "nexus.example.com/ecos-apps:1-snapshot")
	def.Kind = appdef.KindCiteckCore

	r.Start([]appdef.ApplicationDef{def}, false)
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Reset the pull counter AFTER the initial start (which legitimately pulls).
	md.mu.Lock()
	md.pullCalls = 0
	md.mu.Unlock()

	// Trigger T31 self-heal: STOPPING_FAILED with the backoff window elapsed.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.setAppStatus(app, AppStatusStoppingFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("T31 did not self-heal back to RUNNING")
	}

	md.mu.Lock()
	pulls := md.pullCalls
	md.mu.Unlock()
	assert.Zero(t, pulls, "self-heal must NOT pull — recreate reuses the local image")
}

// TestSelfHealRemovedAppNotRevived pins the markedForRemoval guard in T31: an
// app removed from the desired set (by a reload/regenerate) whose stop timed out
// into STOPPING_FAILED must be driven to STOPPED and GC'd — NOT recreated, which
// would resurrect a zombie that survives the very reload that removed it.
func TestSelfHealRemovedAppNotRevived(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def}, false)
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Removed-from-desired + STOPPING_FAILED + backoff elapsed.
	r.mu.Lock()
	app := r.apps[def.Name]
	app.markedForRemoval = true
	r.setAppStatus(app, AppStatusStoppingFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	// Must be removed (STOPPED → T32 GC), never revived to RUNNING.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a := r.FindApp(def.Name)
		if a == nil {
			return // GC'd — correct outcome
		}
		if a.Status == AppStatusRunning {
			t.Fatalf("T31 revived a markedForRemoval app to RUNNING — must go STOPPED+GC")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("markedForRemoval app was not GC'd; final status=%s", r.FindApp(def.Name).Status)
}

// TestReleaseImagePresentNeverPulls pins the invariant for release images
// (no "snapshot" in the tag): if the image is already present locally, no
// scenario pulls it. shouldPullImage==false for a release tag, so runPullTask
// short-circuits on ImageExists (mock default true) — start and self-heal both
// leave pullCalls at zero. Kotlin 1.x parity (AppImagePullAction: skip when
// !pullIfPresent && image present).
func TestReleaseImagePresentNeverPulls(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Citeck-core but a RELEASE tag (no "snapshot") ⇒ shouldPullImage==false.
	def := simpleApp("eapps", "nexus.example.com/ecos-apps:2.26.10")
	def.Kind = appdef.KindCiteckCore

	r.Start([]appdef.ApplicationDef{def}, false)
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Start must not have pulled a present release image.
	md.mu.Lock()
	startPulls := md.pullCalls
	md.mu.Unlock()
	assert.Zero(t, startPulls, "release image present locally must not pull on start")

	// Self-heal must not pull either.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.setAppStatus(app, AppStatusStoppingFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("self-heal did not return release app to RUNNING")
	}
	md.mu.Lock()
	totalPulls := md.pullCalls
	md.mu.Unlock()
	assert.Zero(t, totalPulls, "release image present locally must not pull in any scenario")
}
