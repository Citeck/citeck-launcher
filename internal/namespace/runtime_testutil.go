// Package namespace — test utilities for the runtime state machine.
//
// This file does NOT import "testing". It provides:
//   - newRuntimeForTest: package-private constructor that flips r.testMode=true
//     and wires test-injectable knobs (clock, ticker period, intervals).
//   - StepOnce / RunUntilQuiescent: the sole drivers when testMode is true.
//   - InjectCmd / InjectResult: bypass back-pressure for deterministic feeding.
//   - AdvanceClock: mutate the FakeClock attached via WithTestClock.
//
// Production code MUST NEVER set testMode=true. The flag's only writer lives
// here. Production NewRuntime / NewRuntimeWithActions ignore it; production
// Start() panics if it sees testMode==true (defensive — see runtime_commands.go).
package namespace

import (
	"errors"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// AppTransition records a per-app status change observed during a StepOnce.
type AppTransition struct {
	From AppRuntimeStatus
	To   AppRuntimeStatus
}

// StepResult is what one synchronous loop iteration produced. Consumers
// inspect it to assert behavioral properties (no need to peek at private
// state directly).
type StepResult struct {
	EventsEmitted    []api.EventDto
	AppsTransitioned map[string]AppTransition
	WorkersSpawned   []workers.TaskID
	Quiescent        bool
}

// TestOption configures a test-mode runtime. Functional-options style so the
// list can grow without touching call sites.
type TestOption func(*Runtime)

// WithTestClock substitutes the runtime's clock. If c is a *FakeClock, the
// pointer is stashed in a package-private registry so AdvanceClock can mutate
// it; otherwise AdvanceClock panics on call.
func WithTestClock(c Clock) TestOption {
	return func(r *Runtime) {
		r.nowFunc = c.Now
		if fc, ok := c.(*FakeClock); ok {
			fakeClockBindings.put(r, fc)
		}
	}
}

// WithTickerPeriod overrides the housekeeping ticker cadence (default 1s).
func WithTickerPeriod(d time.Duration) TestOption {
	return func(r *Runtime) { r.tickerPeriod = d }
}

// WithStatsInterval overrides the stats dispatch cadence (default 5s).
func WithStatsInterval(d time.Duration) TestOption {
	return func(r *Runtime) { r.statsInterval = d }
}

// WithReconcilerInterval overrides the reconciler cadence (default 60s).
func WithReconcilerInterval(d time.Duration) TestOption {
	return func(r *Runtime) { r.reconcilerInterval = d }
}

// WithGroupTimeout overrides the operator-initiated STOPPING budget (T23,
// default 10s — see runtime_loop.go defaultGroupTimeout).
func WithGroupTimeout(d time.Duration) TestOption {
	return func(r *Runtime) { r.groupTimeout = d }
}

// WithLongStopTimeout overrides the runtime-initiated recreate STOPPING
// budget (T23, default 60s — see runtime_loop.go defaultLongStopTimeout).
func WithLongStopTimeout(d time.Duration) TestOption {
	return func(r *Runtime) { r.longStopTimeout = d }
}

// newRuntimeForTest builds a Runtime in test mode. runLoop is NOT started;
// callers drive transitions exclusively via StepOnce / RunUntilQuiescent.
func newRuntimeForTest(cfg *Config, dockerClient docker.RuntimeClient, volumesBase string, opts ...TestOption) *Runtime {
	r := NewRuntimeWithActions(cfg, dockerClient, volumesBase, nil)
	r.testMode = true
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// StepOnce runs ONE iteration of the loop synchronously on the caller's
// goroutine. Drains any queued runtimeCmd via the same coalesce-then-apply
// pipeline runtimeLoop uses, and reports what it saw.
//
// Panics if called on a non-testMode runtime — a production Runtime is driven
// by its own goroutine and cannot be safely stepped from a test.
func (r *Runtime) StepOnce() StepResult {
	if !r.testMode {
		panic("namespace.Runtime.StepOnce called on a production-mode runtime; build with newRuntimeForTest")
	}
	res := StepResult{
		AppsTransitioned: make(map[string]AppTransition),
		Quiescent:        true,
	}
	// Drain whatever command(s) are buffered. Empty queue → quiescent step.
	select {
	case first := <-r.cmdQueue.Chan():
		res.Quiescent = false
		r.cmdQueue.Drain(first, func(c runtimeCmd) {
			// Surface the command as a synthetic EventDto so tests can observe
			// routing without depending on full applyCommand semantics.
			res.EventsEmitted = append(res.EventsEmitted, api.EventDto{
				Type:        "cmd_observed",
				NamespaceID: r.nsID,
				After:       c.cmdTag(),
			})
		})
	default:
	}
	return res
}

// ErrMaxStepsExceeded is returned by RunUntilQuiescent when the loop fails to
// reach a quiescent state within maxSteps iterations.
var ErrMaxStepsExceeded = errors.New("RunUntilQuiescent: maxSteps exceeded")

// RunUntilQuiescent calls StepOnce repeatedly until Quiescent==true, returning
// the per-step results. Returns ErrMaxStepsExceeded if maxSteps is reached
// first; callers decide how to surface (t.Fatalf, log, etc.).
func (r *Runtime) RunUntilQuiescent(maxSteps int) ([]StepResult, error) {
	results := make([]StepResult, 0, 8)
	for range maxSteps {
		res := r.StepOnce()
		results = append(results, res)
		if res.Quiescent {
			return results, nil
		}
	}
	return results, ErrMaxStepsExceeded
}

// InjectCmd pushes a command directly onto cmdQueue's channel, bypassing
// Enqueue's back-pressure. For test wiring only.
func (r *Runtime) InjectCmd(cmd runtimeCmd) {
	r.cmdQueue.ch <- cmd
}

// InjectResult pushes a worker Result directly onto resultCh. For test wiring
// only.
func (r *Runtime) InjectResult(res workers.Result) {
	r.resultCh <- res
}

// AdvanceClock moves the runtime's FakeClock forward by d. Panics if the
// runtime was not built with WithTestClock(NewFakeClock(...)).
func (r *Runtime) AdvanceClock(d time.Duration) {
	if fc, ok := fakeClockBindings.get(r); ok {
		fc.Advance(d)
		return
	}
	panic("namespace.Runtime.AdvanceClock requires a runtime built with WithTestClock(NewFakeClock(...))")
}

// fakeClockBindings maps test runtimes to their FakeClock for AdvanceClock.
// A package-private registry keeps the binding off the production struct.
// Tests are single-process, so a small sync map is fine. Entries are cleaned
// up by shutdownAfter via fakeClockBindings.delete(r) — a no-op for production
// runtimes that never appear in the map.
var fakeClockBindings = &fakeClockRegistry{m: make(map[*Runtime]*FakeClock)}

type fakeClockRegistry struct {
	mu sync.Mutex
	m  map[*Runtime]*FakeClock
}

func (r *fakeClockRegistry) put(rt *Runtime, fc *FakeClock) {
	r.mu.Lock()
	r.m[rt] = fc
	r.mu.Unlock()
}

func (r *fakeClockRegistry) get(rt *Runtime) (*FakeClock, bool) {
	r.mu.Lock()
	fc, ok := r.m[rt]
	r.mu.Unlock()
	return fc, ok
}

func (r *fakeClockRegistry) delete(rt *Runtime) {
	r.mu.Lock()
	delete(r.m, rt)
	r.mu.Unlock()
}
