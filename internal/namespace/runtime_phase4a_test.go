// Behavioral tests for the state-machine pull-side transitions:
// T1 (adoption), T2 (READY_TO_PULL→PULLING), T5 (PULLING→READY_TO_START),
// T6 (PULLING→PULL_FAILED). T4 (adopt-after-pull) is not implemented; see
// stepAllApps doc comment.
package namespace

import (
	"runtime"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/stretchr/testify/require"
)

// TestAdoptDoesNotPullOnHashMatch: an already-running container whose hash
// matches the ApplicationDef MUST be adopted (T1) without a pull. T2 never
// fires because the app enters r.apps as RUNNING, not READY_TO_PULL.
func TestAdoptDoesNotPullOnHashMatch(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	// doStart resolves ImageDigest from Docker before computing the hash;
	// mirror that here so the seeded label hash matches.
	hashDef := def
	hashDef.ImageDigest = "sha256:mock-digest-" + def.Image

	md.mu.Lock()
	md.nextID++
	id := "container-adopted"
	md.containers[def.Name] = mockContainer{
		id: id,
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: hashDef.GetHash(),
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	r.Start([]appdef.ApplicationDef{def})

	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	md.mu.Lock()
	pulls := md.pullCalls
	md.mu.Unlock()
	if pulls != 0 {
		t.Fatalf("expected 0 pulls for adopted container, got %d", pulls)
	}

	app := r.FindApp(def.Name)
	if app == nil {
		t.Fatalf("app %q missing from runtime", def.Name)
	}
	if app.Status != AppStatusRunning {
		t.Fatalf("app %q expected RUNNING, got %s", def.Name, app.Status)
	}
	if app.ContainerID != id {
		t.Fatalf("adopted ContainerID mismatch: want %q got %q", id, app.ContainerID)
	}
}

// TestAppStatusIsPullingDuringPull: when a non-local image is rolled out, the
// app MUST report PULLING (not READY_TO_PULL or READY_TO_START) while the
// underlying PullImageWithProgress is in flight. Uses mockDocker.pullBlock to
// park the pull until the assertion completes.
func TestAppStatusIsPullingDuringPull(t *testing.T) {
	md := newMockDocker()
	// Force state-machine T2 via the non-local-image branch: simpleApp uses
	// KindThirdParty (shouldPullImage returns false), so we need
	// !ImageExists to trigger T2.
	md.imageExists = map[string]bool{"ecos-model:2.0": false}
	md.pullBlock = make(chan struct{})

	r := NewRuntime(testConfig(), md, t.TempDir())
	var pullBlockClosed bool
	unblock := func() {
		if !pullBlockClosed {
			close(md.pullBlock)
			pullBlockClosed = true
		}
	}
	defer func() {
		unblock()
		r.Shutdown()
	}()

	def := simpleApp("emodel", "ecos-model:2.0")
	// The test requires the !ImageExists branch (not the shouldPullImage one)
	// to drive T2 — otherwise the imageExists map override would be ignored.
	// Pin the Kind explicitly so a future change to simpleApp's default can't
	// silently shift this test onto a different code path.
	require.Equal(t, appdef.KindThirdParty, def.Kind, "test requires KindThirdParty so pull is driven by !ImageExists branch")
	r.Start([]appdef.ApplicationDef{def})

	// Poll (≤2s) for the app to reach PULLING. T2 should fire on the first
	// stepAllApps iteration after doStart inserts it as READY_TO_PULL.
	if !waitForAppStatus(r, def.Name, AppStatusPulling, 2*time.Second) {
		found := r.FindApp(def.Name)
		status := "nil"
		if found != nil {
			status = string(found.Status)
		}
		t.Fatalf("app did not reach PULLING during blocked pull, got %s", status)
	}

	// Unblock the pull; the app should eventually reach RUNNING.
	unblock()

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		found := r.FindApp(def.Name)
		status := "nil"
		if found != nil {
			status = string(found.Status)
		}
		t.Fatalf("app did not reach RUNNING after unblock, got %s", status)
	}
}

// TestAdoptDoesNotLeakLegacyPullAndStartApp verifies that an adopted
// container (T1: hash match → RUNNING) does not leak goroutines.
//
// Invariant: the runtime.NumGoroutine delta after shutdown must converge to
// the pre-Start baseline within a bounded window.
func TestAdoptDoesNotLeakLegacyPullAndStartApp(t *testing.T) {
	md := newMockDocker()
	def := simpleApp("postgres", "postgres:17")
	hashDef := def
	hashDef.ImageDigest = "sha256:mock-digest-" + def.Image
	md.mu.Lock()
	md.nextID++
	md.containers[def.Name] = mockContainer{
		id: "container-adopted",
		labels: map[string]string{
			docker.LabelAppName: def.Name,
			docker.LabelAppHash: hashDef.GetHash(),
			"citeck.launcher":   "true",
		},
	}
	md.mu.Unlock()

	baseline := runtime.NumGoroutine()

	r := NewRuntime(testConfig(), md, t.TempDir())
	r.Start([]appdef.ApplicationDef{def})

	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 2*time.Second) {
		t.Fatalf("app %q did not reach RUNNING via adoption", def.Name)
	}

	// Goroutine counts should converge after shutdown. Tolerance is generous
	// because NumGoroutine is noisy and the runtime model has multiple
	// long-lived goroutines (runtimeLoop, dispatcher, eventDispatchLoop,
	// reconciler).
	r.Shutdown()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+8 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if delta := runtime.NumGoroutine() - baseline; delta > 8 {
		t.Fatalf("goroutine leak after adopt+shutdown: baseline=%d now=%d (delta=%d)",
			baseline, runtime.NumGoroutine(), delta)
	}
}
