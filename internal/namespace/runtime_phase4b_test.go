// Behavioral tests for the state-machine start-side transitions:
// T7 (deps→STARTING), T8/T9 (DEPS_WAITING gating), T10–T16b (init/start/probe
// chain), T24 (PULL_FAILED auto-retry), T25 (START_FAILED auto-retry).
package namespace

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initTrackingDocker records CreateContainer call order so tests can assert
// the T11/T12 init-chain dispatched init containers serially before the main
// start. The init chain produces containers named "{app}-init"; the main
// start produces "{app}".
type initTrackingDocker struct {
	*mockDocker
	createOrder []string // appended container names in CreateContainer call order
}

func (d *initTrackingDocker) CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error) {
	d.mu.Lock()
	d.createOrder = append(d.createOrder, app.Name)
	d.mu.Unlock()
	return d.mockDocker.CreateContainer(ctx, app, volumesBaseDir)
}

// TestPullFailedBackoff exercises T24: an app in PULL_FAILED with retryState
// {count:1, lastAttempt:now} must NOT auto-retry until the backoff window
// (min(1<<(count-1) min, 10 min) = 1 min for count=1) has elapsed. Once the
// lastAttempt is far enough in the past, T24 fires on the next stepAllApps
// iteration and transitions PULL_FAILED → READY_TO_PULL.
//
// Drives through the production runtime (no testMode) — sets retryState
// directly on the live runtime, then observes the transition via a polling
// helper. Using past-dated lastAttempt avoids a fake-clock dependency.
func TestPullFailedBackoff(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Force the app into PULL_FAILED with a retry counter and a recent
	// lastAttempt. State machine T24 must NOT promote it to READY_TO_PULL
	// while the backoff window (min(1<<(1-1) min, 10 min) = 1 min) has not
	// elapsed.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.setAppStatus(app, AppStatusPullFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now()},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	// T24 must NOT fire because the backoff window is 1 minute from now.
	// Never() polls for 1500ms and fails only if the status ever changes.
	assert.Never(t, func() bool {
		return r.FindApp(def.Name).Status != AppStatusPullFailed
	}, 1500*time.Millisecond, 50*time.Millisecond,
		"T24 fired prematurely: status left PULL_FAILED during backoff window")

	// Move lastAttempt 20 minutes into the past — backoff (1 min) is now
	// well exceeded. Next stepAllApps iteration must fire T24.
	r.mu.Lock()
	r.retryState[def.Name] = retryInfo{count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)}
	r.mu.Unlock()
	r.signalCh.Flush()

	// After T24 fires, the app transitions to READY_TO_PULL → T2 → PULLING
	// → T5 → READY_TO_START → T7 → STARTING → T15 → RUNNING. Any of those
	// later statuses confirms T24 fired (mock pull/start are non-blocking).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := r.FindApp(def.Name).Status
		if status != AppStatusPullFailed {
			// T24 fired — advanced past PULL_FAILED.
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("T24 did not fire within 5s — app stuck at PULL_FAILED")
}

// TestStartFailedBackoff exercises T25: the symmetric path for START_FAILED.
// Same pattern as TestPullFailedBackoff but with START_FAILED → READY_TO_START.
func TestStartFailedBackoff(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("postgres", "postgres:17")
	r.Start([]appdef.ApplicationDef{def})
	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		t.Fatalf("app did not reach RUNNING for setup")
	}

	// Force the app into START_FAILED with a recent retry attempt.
	r.mu.Lock()
	app := r.apps[def.Name]
	r.setAppStatus(app, AppStatusStartFailed)
	r.retryState = map[string]retryInfo{
		def.Name: {count: 1, lastAttempt: time.Now()},
	}
	r.mu.Unlock()
	r.signalCh.Flush()

	// Within the backoff window — T25 must not fire.
	assert.Never(t, func() bool {
		return r.FindApp(def.Name).Status != AppStatusStartFailed
	}, 1500*time.Millisecond, 50*time.Millisecond,
		"T25 fired prematurely: status left START_FAILED during backoff window")

	// Move lastAttempt past the window — T25 fires.
	r.mu.Lock()
	r.retryState[def.Name] = retryInfo{count: 1, lastAttempt: time.Now().Add(-20 * time.Minute)}
	r.mu.Unlock()
	r.signalCh.Flush()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := r.FindApp(def.Name).Status
		if status != AppStatusStartFailed {
			return // T25 advanced the app
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("T25 did not fire within 5s — app stuck at START_FAILED")
}

// TestInitContainerSequence pins the T11/T12 init-chain ordering: an app with
// 2 init containers must dispatch them serially (init #1, init #2) before
// CreateContainer for the main container. Asserts on createOrder so the
// happens-before relationship is observable, not just the count.
func TestInitContainerSequence(t *testing.T) {
	md := newMockDocker()
	tracker := &initTrackingDocker{mockDocker: md}
	r := NewRuntime(testConfig(), tracker, t.TempDir())
	defer r.Shutdown()

	def := simpleApp("web", "web:1")
	def.InitContainers = []appdef.InitContainerDef{
		{Image: "init-a:1"},
		{Image: "init-b:1"},
	}
	r.Start([]appdef.ApplicationDef{def})

	if !waitForAppStatus(r, def.Name, AppStatusRunning, 10*time.Second) {
		found := r.FindApp(def.Name)
		status := "nil"
		if found != nil {
			status = string(found.Status)
		}
		t.Fatalf("app %q did not reach RUNNING (status=%s)", def.Name, status)
	}

	tracker.mu.Lock()
	order := append([]string(nil), tracker.createOrder...)
	tracker.mu.Unlock()

	// Init containers share the "{appName}-init" name; the main container
	// has just "{appName}". Filter to count both.
	var (
		initIndices []int
		mainIndex   = -1
	)
	for i, name := range order {
		switch name {
		case def.Name + "-init":
			initIndices = append(initIndices, i)
		case def.Name:
			mainIndex = i
		}
	}

	require.Len(t, initIndices, 2, "expected 2 init container creates, got order=%v", order)
	require.NotEqual(t, -1, mainIndex, "expected main container create, got order=%v", order)
	assert.Less(t, initIndices[0], initIndices[1],
		"init #1 must be created before init #2 (T11), got order=%v", order)
	assert.Less(t, initIndices[1], mainIndex,
		"main container must be created after last init (T12), got order=%v", order)
}

// TestDepsWaitThenStart pins the T7→T8→T9 path: an app B that depends on A
// must transition through DEPS_WAITING when started before A is RUNNING, and
// then advance to RUNNING after A becomes RUNNING. Captures app_status events
// to assert the DEPS_WAITING transition is observable (not just elided).
func TestDepsWaitThenStart(t *testing.T) {
	md := newMockDocker()
	// Pull blocks A so B's DEPS_WAITING is observable before A reaches RUNNING.
	md.imageExists = map[string]bool{"image-a:1": false, "image-b:1": true}
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

	// Capture every app_status event for "app-b" so we can scan for DEPS_WAITING.
	var (
		mu        sync.Mutex
		bStatuses []string
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "app_status" || evt.AppName != "app-b" {
			return
		}
		mu.Lock()
		bStatuses = append(bStatuses, evt.After)
		mu.Unlock()
	})

	apps := []appdef.ApplicationDef{
		simpleApp("app-a", "image-a:1"),
		simpleApp("app-b", "image-b:1", "app-a"),
	}
	r.Start(apps)

	// Wait for B to reach DEPS_WAITING — the pull on A is still blocked, so
	// A is PULLING and B (image local) hits T8.
	deadline := time.Now().Add(5 * time.Second)
	gotDepsWaiting := false
	for time.Now().Before(deadline) {
		mu.Lock()
		gotDepsWaiting = slices.Contains(bStatuses, string(AppStatusDepsWaiting))
		mu.Unlock()
		if gotDepsWaiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gotDepsWaiting {
		mu.Lock()
		seen := strings.Join(bStatuses, ",")
		mu.Unlock()
		t.Fatalf("app-b never transitioned to DEPS_WAITING (status sequence: %s)", seen)
	}

	// Unblock A's pull — A reaches RUNNING, T9 fires for B.
	unblock()

	if !waitForAppStatus(r, "app-b", AppStatusRunning, 10*time.Second) {
		t.Fatalf("app-b did not reach RUNNING after dep satisfied")
	}

	// A must also have reached RUNNING.
	a := r.FindApp("app-a")
	if a == nil || a.Status != AppStatusRunning {
		t.Fatalf("app-a should be RUNNING; got %v", a)
	}
}
