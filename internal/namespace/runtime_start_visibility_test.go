package namespace

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestDoStart_AppsVisibleBeforeSnapshotPrePull pins the start-latency contract
// behind the user-visible bug "все висит в Остановлен какое-то время (до
// десятков секунд) и только потом статусы начинают меняться".
//
// doStart used to publish nothing until its phase-3 commit: r.apps was left
// empty (Runtime.Start blanks it) while phase-1 I/O ran — and the dominant
// term there is refreshSnapshotDigests, which pulls EVERY :snapshot image at
// concurrency 4 with a 2-minute per-image cap. Because the runtimeLoop calls
// applyCommand inline, flushEvents does not run until doStart returns either,
// so even the buffered STOPPED→STARTING namespace event was withheld. With
// r.apps empty the daemon backfills the whole catalog as STOPPED
// (routes_config.go appDefsToStoppedApps), so every poll during that window
// renders "Остановлен" for every service.
//
// The contract: by the time the pre-pull blocks the loop, the namespace is
// STARTING and every non-detached app is already present in r.apps with a
// non-STOPPED status.
func TestDoStart_AppsVisibleBeforeSnapshotPrePull(t *testing.T) {
	md := newMockDocker()
	apps := []appdef.ApplicationDef{
		simpleApp("gateway", "citeck/gateway:snapshot"),
		simpleApp("emodel", "citeck/emodel:snapshot"),
	}

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Seeding r.apps is only half the fix: the events must actually be DELIVERED
	// before the pre-pull blocks the loop, which is what the explicit
	// flushEvents call buys. Without it they sit in eventBuffer until doStart
	// returns and the UI only catches up on its 3s poll — so observe delivery,
	// not just in-memory state.
	var (
		mu        sync.Mutex
		once      sync.Once
		delivered = map[string]bool{}
		nsStatus  NsRuntimeStatus
		statuses  map[string]AppRuntimeStatus
		flushed   bool
	)
	r.SetEventCallback(func(e api.EventDto) {
		mu.Lock()
		delivered[e.Type] = true
		mu.Unlock()
	})
	deliveredBoth := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return delivered["namespace_status"] && delivered["app_status"]
	}
	r.refreshSnapshotDigestsFn = func(_ context.Context, defs []appdef.ApplicationDef) {
		once.Do(func() {
			// Delivery is asynchronous (dispatchLoop drains eventCh), so poll
			// briefly rather than sampling a single instant.
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				if deliveredBoth() {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			flushed = delivered["namespace_status"] && delivered["app_status"]
			nsStatus = r.Status()
			statuses = make(map[string]AppRuntimeStatus, len(defs))
			for _, d := range defs {
				if a := r.FindApp(d.Name); a != nil {
					statuses[d.Name] = a.Status
				}
			}
		})
	}

	r.Start(apps, true)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("runtime did not reach RUNNING, got %v", r.Status())
	}

	mu.Lock()
	defer mu.Unlock()
	if statuses == nil {
		t.Fatal("refreshSnapshotDigestsFn was never called; refreshImages=true should pre-pull")
	}
	if nsStatus != NsStatusStarting {
		t.Errorf("namespace status during pre-pull = %v, want %v", nsStatus, NsStatusStarting)
	}
	if !flushed {
		t.Error("no namespace_status + app_status events were DELIVERED before the pre-pull blocked the loop — " +
			"doStart seeded the state but never flushed it, so the UI waits for its 3s poll instead")
	}
	for _, d := range apps {
		got, ok := statuses[d.Name]
		if !ok {
			t.Errorf("app %q absent from r.apps while the :snapshot pre-pull blocked the runtime loop — "+
				"the daemon backfills the catalog as STOPPED, so the UI shows every service as "+
				"\"Остановлен\" for the whole pull", d.Name)
			continue
		}
		if got == AppStatusStopped {
			t.Errorf("app %q status during pre-pull = %v, want a non-STOPPED (queued/pulling) status", d.Name, got)
		}
	}
}

// TestDoRegenerate_NamespaceStartingBeforeSnapshotPrePull is the doRegenerate
// half of the same latency contract. Update & Start on an already-RUNNING
// namespace goes doReloadEx → Regenerate(refreshImages=true) → doRegenerate,
// whose pre-pull blocks the loop exactly like doStart's. setStatus(STARTING)
// used to run in the phase-2 lock block AFTER the pre-pull, so the namespace
// still read RUNNING and the user got no signal that the click did anything.
func TestDoRegenerate_NamespaceStartingBeforeSnapshotPrePull(t *testing.T) {
	md := newMockDocker()
	apps := []appdef.ApplicationDef{simpleApp("gateway", "citeck/gateway:snapshot")}

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	r.Start(apps, false)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("runtime did not reach RUNNING, got %v", r.Status())
	}

	var (
		mu       sync.Mutex
		once     sync.Once
		observed NsRuntimeStatus
		called   bool
	)
	r.refreshSnapshotDigestsFn = func(_ context.Context, _ []appdef.ApplicationDef) {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			observed, called = r.Status(), true
		})
	}

	r.Regenerate(apps, nil, nil, true)
	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING after regenerate, got %v", r.Status())
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("refreshSnapshotDigestsFn was never called; refreshImages=true should pre-pull")
	}
	if observed != NsStatusStarting {
		t.Errorf("namespace status during pre-pull = %v, want %v — the user gets no feedback "+
			"that Update & Start did anything while the pre-pull blocks the loop", observed, NsStatusStarting)
	}
}

// TestDoStart_DetachedAppStaysStoppedInEarlySeed guards the seed added for the
// test above: a detached (manually stopped) app must NOT be advertised as
// queued-to-start just because the pre-pull window now publishes app state.
// Detached apps are excluded from start and are skipped by stepAllApps, so
// showing them as queued would render a service that never moves.
func TestDoStart_DetachedAppStaysStoppedInEarlySeed(t *testing.T) {
	md := newMockDocker()
	apps := []appdef.ApplicationDef{
		simpleApp("gateway", "citeck/gateway:snapshot"),
		simpleApp("emodel", "citeck/emodel:snapshot"),
	}

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	r.mu.Lock()
	r.manualStoppedApps = map[string]bool{"emodel": true}
	r.mu.Unlock()

	var (
		mu       sync.Mutex
		once     sync.Once
		statuses map[string]AppRuntimeStatus
	)
	r.refreshSnapshotDigestsFn = func(_ context.Context, defs []appdef.ApplicationDef) {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			statuses = make(map[string]AppRuntimeStatus, len(defs))
			for _, d := range defs {
				if a := r.FindApp(d.Name); a != nil {
					statuses[d.Name] = a.Status
				}
			}
		})
	}

	r.Start(apps, true)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("runtime did not reach RUNNING, got %v", r.Status())
	}

	mu.Lock()
	defer mu.Unlock()
	if statuses == nil {
		t.Fatal("refreshSnapshotDigestsFn was never called")
	}
	if got := statuses["emodel"]; got != AppStatusStopped {
		t.Errorf("detached app status during pre-pull = %v, want %v", got, AppStatusStopped)
	}
	if got := statuses["gateway"]; got == AppStatusStopped {
		t.Errorf("non-detached app status during pre-pull = %v, want non-STOPPED", got)
	}
}
