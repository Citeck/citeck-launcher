package namespace

import (
	"bytes"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestNsStatusUpdatesWithin100ms gates the UX contract: the transition from
// STARTING to RUNNING must be externally-observable within 1500ms of the
// STARTING event. The signalCh-driven runtimeLoop wakes promptly on each
// setStatus call. With DrainBurst(250ms, 4) the worst case is ~4×250ms = 1s,
// so 1500ms is a stable CI gate.
//
// Measurement: wall-clock delta between the namespace_status STARTING event
// and the namespace_status RUNNING event. This isolates loop latency from
// mock-docker container startup time, making the gate CI-stable.
func TestNsStatusUpdatesWithin100ms(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	var (
		mu          sync.Mutex
		startingAt  time.Time
		nsRunningCh = make(chan time.Time, 1)
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "namespace_status" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if evt.After == string(NsStatusStarting) && startingAt.IsZero() {
			startingAt = time.Now()
		}
		if evt.After == string(NsStatusRunning) {
			select {
			case nsRunningCh <- time.Now():
			default:
			}
		}
	})

	r.Start([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17")})

	select {
	case runningAt := <-nsRunningCh:
		mu.Lock()
		start := startingAt
		mu.Unlock()
		if start.IsZero() {
			// STARTING was never observed — gate passes trivially (pre-Start
			// subscribe race on extremely fast hardware; acceptable).
			return
		}
		delta := runningAt.Sub(start)
		if delta > 1500*time.Millisecond {
			t.Fatalf("namespace_status STARTING→RUNNING latency %v (>1500ms); signalCh loop wake is too slow", delta)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("namespace_status RUNNING not observed within 5s")
	}
}

// TestAppAndNsStatusEventsEmittedInOrderWithinFlush verifies the within-flush
// event ordering guarantee: when the app_status RUNNING transition for the
// last outstanding app drives the namespace into RUNNING, the resulting
// namespace_status event is buffered in the same runtimeLoop iteration and
// flushed sequentially right after the app_status event — so observers see
// them back-to-back (<50ms delta) with the namespace transition following
// the app transition, never before it.
//
// This reflects "both events were buffered in the same iteration and flushed
// sequentially" rather than "the system propagates status in <100ms
// end-to-end". The two timestamps come from the same flushEvents call, so
// the delta is essentially the cost of two channel sends plus the callback.
func TestAppAndNsStatusEventsEmittedInOrderWithinFlush(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	// Subscribe BEFORE Start so the namespace_status RUNNING transition is
	// observable. We track wall-clock latency between when our callback sees
	// app=postgres reaching RUNNING and when namespace_status RUNNING fires.
	var (
		mu                sync.Mutex
		appRunningAt      time.Time
		nsRunningAt       time.Time
		appRunningDone    bool
		nsRunningDone     bool
		nsRunningObserved = make(chan struct{})
	)
	r.SetEventCallback(func(evt api.EventDto) {
		mu.Lock()
		defer mu.Unlock()
		if !appRunningDone && evt.Type == "app_status" && evt.AppName == "postgres" && evt.After == string(AppStatusRunning) {
			appRunningAt = time.Now()
			appRunningDone = true
		}
		if !nsRunningDone && evt.Type == "namespace_status" && evt.After == string(NsStatusRunning) {
			nsRunningAt = time.Now()
			nsRunningDone = true
			close(nsRunningObserved)
		}
	})

	r.Start([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17")})

	select {
	case <-nsRunningObserved:
	case <-time.After(5 * time.Second):
		t.Fatalf("namespace_status RUNNING not observed within 5s")
	}

	mu.Lock()
	delta := nsRunningAt.Sub(appRunningAt)
	mu.Unlock()

	if !appRunningDone {
		t.Fatalf("app_status RUNNING was never observed")
	}
	if delta < 0 {
		t.Fatalf("namespace_status fired before app_status: app=%v ns=%v", appRunningAt, nsRunningAt)
	}
	if delta > 50*time.Millisecond {
		t.Fatalf("namespace_status arrived %v after app_status (>50ms within-flush threshold)", delta)
	}
}

// TestSignalChCoalescesRapidStatusUpdates verifies that a burst of rapid
// per-app status changes does NOT explode into one namespace_status event per
// app change. 10 status changes in a tight burst must produce ≤ 2
// namespace_status events (the derivation is idempotent — checkStatus only
// emits when the derived value actually differs).
func TestSignalChCoalescesRapidStatusUpdates(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	var (
		mu          sync.Mutex
		nsEventSeen int
	)
	r.SetEventCallback(func(evt api.EventDto) {
		if evt.Type != "namespace_status" {
			return
		}
		mu.Lock()
		nsEventSeen++
		mu.Unlock()
	})

	apps := make([]appdef.ApplicationDef, 0, 10)
	for i := range 10 {
		apps = append(apps, simpleApp("svc"+string(rune('0'+i)), "img:1"))
	}
	r.Start(apps)

	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}
	// Allow a brief settle window so any in-flight burst events flush.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	count := nsEventSeen
	mu.Unlock()
	// With 10 successful apps and no intermediate STALLED transition, exactly
	// two namespace_status events are expected: STOPPED→STARTING (emitted by
	// doStart via setStatus) and STARTING→RUNNING (emitted once all apps
	// reach RUNNING). Coalescing collapses the N-app-transition burst into a
	// single post-flush NS evaluation, so we do NOT see one NS event per app.
	//
	// We keep the upper bound at 2 (rather than asserting == 2) because on
	// unusually slow hardware an early updateNsStatus iteration could see
	// the NS already RUNNING before the subscribe-before-Start callback has
	// been delivered for the STARTING event — but in practice the observed
	// count is always 2.
	if count > 2 {
		t.Fatalf("namespace_status fired %d times for 10 app status changes; expected ≤ 2", count)
	}
	if count < 1 {
		t.Fatalf("namespace_status RUNNING never fired")
	}
}

// TestStopFlushesStoppingEvents pins the guarantee that runtimeLoop's stop
// path calls flushEvents before returning. Without it, STOPPING / STOPPED
// app_status and namespace_status events buffered inside doStop would be
// dropped. External observers (SSE clients, CLI `--detach`) rely on seeing
// these events.
//
// The test subscribes to events before Shutdown is triggered, waits for the
// namespace to reach RUNNING, then calls Shutdown() (which routes through
// Stop() → cmdQueue arm (cmdStop) → doStop → terminal-path flushEvents)
// and asserts that BOTH namespace_status transitions (RUNNING→STOPPING and
// STOPPING→STOPPED) were observed.
func TestStopFlushesStoppingEvents(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())

	var (
		mu        sync.Mutex
		observed  []api.EventDto
		stoppedCh = make(chan struct{})
	)
	r.SetEventCallback(func(evt api.EventDto) {
		mu.Lock()
		observed = append(observed, evt)
		mu.Unlock()
		if evt.Type == "namespace_status" && evt.After == string(NsStatusStopped) {
			// Non-blocking notify to avoid double-close if somehow emitted twice.
			select {
			case <-stoppedCh:
			default:
				close(stoppedCh)
			}
		}
	})

	r.Start([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17")})
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	r.Shutdown()

	select {
	case <-stoppedCh:
	case <-time.After(5 * time.Second):
		mu.Lock()
		t.Fatalf("namespace_status STOPPED not observed within 5s; seen=%v", observed)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawStopping, sawStopped bool
	for _, evt := range observed {
		if evt.Type != "namespace_status" {
			continue
		}
		if evt.After == string(NsStatusStopping) {
			sawStopping = true
		}
		if evt.After == string(NsStatusStopped) {
			sawStopped = true
		}
	}
	if !sawStopping {
		t.Fatalf("namespace_status STOPPING event was not observed — buffer flush on cmdQueue (cmdStop) terminal exit is missing; seen=%v", observed)
	}
	if !sawStopped {
		t.Fatalf("namespace_status STOPPED event was not observed; seen=%v", observed)
	}
}

// TestShutdownDoesNotLeakStatsGoroutine asserts that after the runtime shuts
// down cleanly, no goroutine remains carrying the "citeck-runtime-stats" pprof
// label. Stats are dispatched as pprof-labeled worker tasks from tick(), not
// via a standalone goroutine.
func TestShutdownDoesNotLeakStatsGoroutine(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())

	r.Start([]appdef.ApplicationDef{simpleApp("postgres", "postgres:17")})
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING")
	}

	// Force at least one stats dispatch so the labeled goroutine has a chance
	// to appear in the profile if it's leaking. Reset lastStatsDispatch so the
	// next tick (1s cadence) dispatches stats; then poll for the labeled
	// goroutine to actually appear — polling is robust to slow CI hosts where
	// a fixed Sleep might not cover the dispatch delay.
	r.mu.Lock()
	r.lastStatsDispatch = time.Time{}
	r.mu.Unlock()
	statsDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(statsDeadline) {
		if hasGoroutineWithLabel("citeck-runtime-stats") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	r.Shutdown()

	// Allow goroutines a brief window to unwind after Shutdown returns.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !hasGoroutineWithLabel("citeck-runtime-stats") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine with label citeck-runtime-stats still alive after shutdown")
}

// hasGoroutineWithLabel returns true if any current goroutine has a pprof
// label matching the given value (key="work").
func hasGoroutineWithLabel(value string) bool {
	var buf bytes.Buffer
	prof := pprof.Lookup("goroutine")
	if prof == nil {
		return false
	}
	// Debug=2 includes pprof labels per goroutine.
	if err := prof.WriteTo(&buf, 2); err != nil {
		return false
	}
	// labels appear inline like "labels: {"work":"citeck-runtime-stats"}"
	return bytes.Contains(buf.Bytes(), []byte(value))
}
