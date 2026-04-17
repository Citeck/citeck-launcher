// Package namespace implements the Citeck namespace runtime as a
// single-threaded state machine driven by a signal-queue wake-up loop.
//
// # Architecture
//
// One runtimeLoop goroutine owns every mutation to r.apps, per-app status,
// namespace status, and persistence state. External callers (HTTP handlers,
// tests, shim) enqueue typed commands via cmdQueue; workers (pull / start /
// stop / probe / stats / reconcile / liveness) run off-loop on the dispatcher
// and post typed Results back on resultCh; applyWorkerResult applies
// state-machine transitions under r.mu. Per-iteration stepAllApps walks all
// non-detached apps for transitions (T1–T33).
//
//	┌─────────── HTTP handlers ──────────┐      ┌── tests (step runner) ──┐
//	│  Start/Stop/Regen/StopApp/…         │      │  InjectCmd(…)            │
//	└──────────────────┬──────────────────┘      └──────────┬──────────────┘
//	                   │                                    │
//	                   ▼                                    ▼
//	          ┌────────────────────────────────────────────────────┐
//	          │                cmdQueue (typed, FIFO)              │
//	          │   buffer 256; 500 ms back-pressure; coalescing     │
//	          └─────────────────────────┬──────────────────────────┘
//	                                    │
//	                                    ▼
//	       ┌────────────────────── runtimeLoop() ────────────────────────┐
//	       │              (single goroutine, owns state)                  │
//	       └────────────────┬─────────────────────────────────────────────┘
//	                        │ dispatches workers (no lock held)
//	                        ▼
//	              ┌─────────────────────────────┐
//	              │   worker task functions     │
//	              │  pullImage / startContainer │
//	              │  stopContainer / initCont.  │
//	              │  livenessProbe/startupProbe │
//	              │  removeNetwork / stats      │
//	              └──────────────┬──────────────┘
//	                             │ post Result to resultCh + signalCh.Flush()
//	                             ▼
//	                   runtimeLoop processes result
//
// # Concurrency rules
//
//   - Only runtimeLoop writes to r.apps, per-app Status, namespace status,
//     retryState, livenessFailures, and persistence. Worker results are
//     always applied on-loop via applyWorkerResult.
//   - Workers receive value-copied inputs only (app name, def, snapshot).
//     They must NOT read r.apps or mutate AppRuntime fields. They may call
//     r.docker.* and r.registryAuthFn.Load() (atomic).
//   - Workers must NOT emit events. All event buffering goes through
//     setAppStatus / setStatus / emitRestartEvent on runtimeLoop.
//     flushEvents drains the buffer once per iteration in append order.
//   - runtimeLoop calls stepAllApps() after every select case — a
//     transition committed in this iteration (e.g. A → RUNNING) is
//     observed by its dependent (B: DEPS_WAITING → STARTING) in the
//     same iteration, no separate wake-up channel needed.
//   - The dispatcher's task table is logically owned by runtimeLoop.
//     External API goroutines (StopApp, StartApp, etc.) may call
//     Dispatch/Cancel under the dispatcher's internal Mutex.
//   - testMode runtimes skip runtimeLoop entirely; tests drive the
//     state machine via StepOnce / RunUntilQuiescent.
package namespace

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// NsRuntimeStatus represents namespace lifecycle states.
type NsRuntimeStatus string

// Namespace lifecycle states. Values are sourced from the api package so the
// DTO wire format (api.NsStatus*) and the internal typed enum never drift.
const (
	NsStatusStopped  NsRuntimeStatus = api.NsStatusStopped
	NsStatusStarting NsRuntimeStatus = api.NsStatusStarting
	NsStatusRunning  NsRuntimeStatus = api.NsStatusRunning
	NsStatusStopping NsRuntimeStatus = api.NsStatusStopping
	NsStatusStalled  NsRuntimeStatus = api.NsStatusStalled
)

// AppRuntimeStatus represents per-app lifecycle states.
type AppRuntimeStatus string

// Per-app lifecycle states. Values sourced from api.AppStatus* (single
// source of truth for the wire format — see api/dto.go).
const (
	AppStatusReadyToPull    AppRuntimeStatus = api.AppStatusReadyToPull
	AppStatusPulling        AppRuntimeStatus = api.AppStatusPulling
	AppStatusPullFailed     AppRuntimeStatus = api.AppStatusPullFailed
	AppStatusReadyToStart   AppRuntimeStatus = api.AppStatusReadyToStart
	AppStatusDepsWaiting    AppRuntimeStatus = api.AppStatusDepsWaiting
	AppStatusStarting       AppRuntimeStatus = api.AppStatusStarting
	AppStatusRunning        AppRuntimeStatus = api.AppStatusRunning
	AppStatusFailed         AppRuntimeStatus = api.AppStatusFailed
	AppStatusStartFailed    AppRuntimeStatus = api.AppStatusStartFailed
	AppStatusStopping       AppRuntimeStatus = api.AppStatusStopping
	AppStatusStoppingFailed AppRuntimeStatus = api.AppStatusStoppingFailed
	AppStatusStopped        AppRuntimeStatus = api.AppStatusStopped
)

// AppRuntime holds the state for a single app.
type AppRuntime struct {
	Name         string
	Status       AppRuntimeStatus
	StatusText   string
	Def          appdef.ApplicationDef
	ContainerID  string
	CPU          string
	Memory       string
	RestartCount int

	// Non-persisted state-machine fields; zero-value by default.
	// Only runtimeLoop may write these — including tick()-dispatched
	// handleReconcileDiffResult / handleLivenessProbeResult which apply
	// worker outcomes under r.mu.Lock on the runtimeLoop goroutine.
	desiredNext       AppRuntimeStatus // T17a/T30/cmdRestartApp restart routing via T21.
	initialSweep      bool             // set for stale-container sweep and reload-changed-hash recreate; tickUnderLock picks longStopTimeout when true.
	markedForRemoval  bool             // set by cmdRegenerate for apps removed from the desired set; stepAllApps T32 deletes the entry once STOPPED.
	stoppingStartedAt time.Time        // set on STOPPING transition; tick() T23 budget enforcement.
	initStepIdx       int              // ephemeral: current init-container index during STARTING (init phase).
}

// EventCallback is called when namespace or app state changes.
type EventCallback func(event api.EventDto)

// RegistryAuthFunc returns registry credentials for a given image, or nil if none.
type RegistryAuthFunc func(image string) *docker.RegistryAuth

// Runtime manages the full namespace lifecycle.
// All mutable state is protected by mu. setStatus/setAppStatus must only be called
// while mu is held by the caller.
type Runtime struct {
	mu                    sync.RWMutex
	status                NsRuntimeStatus
	config                *Config
	apps                  map[string]*AppRuntime
	docker                docker.RuntimeClient
	running               atomic.Bool
	nsID                  string
	volumesBase           string
	eventCb               atomic.Pointer[EventCallback]
	eventCh               chan api.EventDto
	registryAuthFn        atomic.Pointer[RegistryAuthFunc]
	history               *OperationHistory
	manualStoppedApps     map[string]bool
	editedApps            map[string]appdef.ApplicationDef // user-edited app defs
	editedLockedApps      map[string]bool                  // locked edits survive regeneration
	dependsOnDetachedApps map[string]bool                  // detached apps that trigger regen on restart
	lastApps              []appdef.ApplicationDef          // last app defs passed to doStart
	cachedBundle          *bundle.Def                      // last successfully resolved bundle (persisted)
	retryState            map[string]retryInfo             // retry tracking for failed apps
	livenessFailures      map[string]int                   // consecutive liveness probe failure counts
	restartCounts         map[string]int                   // total restart counts per app
	restartEvents         []RestartEvent                   // ring buffer of restart events
	reconcilerCfg         *ReconcilerConfig                // optional override from daemon.yml
	reconcilerEnabled     bool                             // gate for reconcile-diff dispatch from tickUnderLock; default true, flipped by SetReconcilerConfig when daemon.yml sets reconciler.enabled: false.
	livenessEnabled       bool                             // gate for per-app liveness probe dispatch from tickUnderLock; default true, flipped by SetReconcilerConfig when daemon.yml sets reconciler.livenessEnabled: false.
	defaultStopTimeout    int                              // from daemon.yml docker.stopTimeout; 0 = Docker's own 10s SIGTERM→SIGKILL default applies (see tickUnderLock T23)
	teardownOnce          sync.Once                        // guards shutdownAfter (full teardown path)
	signalOnce            sync.Once                        // guards signalShutdown (close shutdownComplete)
	// detaching is set by doDetach BEFORE CancelAll(CancelDetach). It fences
	// stepAllAppsUnderLock and tickUnderLock against dispatching new workers
	// on the detaching runtime: the current runtimeLoop iteration still runs
	// its tail (stepAllApps / tick / evaluateContinuations / updateNsStatus)
	// after applyCommand returns for cmdDetach, and without this guard
	// pre-RUNNING apps (READY_TO_PULL / DEPS_WAITING / START_FAILED /
	// PULL_FAILED) could spawn pull/start/liveness/reconcile workers on the
	// dispatcher's Background context that survive detach. Semantically
	// "detaching" is not NsStatusStopping (which drives the graceful-shutdown
	// group chain), so a dedicated flag is clearer than overloading r.status.
	detaching atomic.Bool
	runCtx    context.Context // set by doStart, canceled by doStop
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	signalCh             *SignalQueue
	cmdQueue             *CmdQueue
	resultCh             chan workers.Result
	dirty                atomic.Bool      // flipped by state mutators; runtimeLoop tail coalesces into one persistState per iteration.
	nowFunc              func() time.Time // returns current time (test-injectable via WithTestClock).
	dispatcher           *Dispatcher
	pendingContinuations []continuation
	shutdownComplete     chan struct{}
	// eventBuffer is the per-iteration event accumulator. flushEvents drains it
	// to eventCh in append order at the end of each runtimeLoop iteration.
	// All appenders MUST hold r.mu.Lock for the duration of the append.
	// flushEvents reads under r.mu.Lock, so the buffer is consistently
	// protected by r.mu.
	eventBuffer           []api.EventDto
	tickerPeriod          time.Duration        // housekeeping cadence (default 1s).
	statsInterval         time.Duration        // stats dispatch cadence (default 5s).
	reconcilerInterval    time.Duration        // reconciler-diff dispatch cadence (default 60s).
	lastStatsDispatch     time.Time            // updated by tick().
	lastReconcileDispatch time.Time            // reconciler-diff scheduling.
	livenessNextAt        map[string]time.Time // per-app liveness probe schedule.
	// Tick-driven STOPPING budgets (T23). groupTimeout = operator-initiated
	// cmdStopApp / cmdStop budget (default 10s); longStopTimeout = runtime-
	// initiated recreate budget (default 60s — accommodates Java SIGTERM
	// handlers; flagged on the app via initialSweep).
	groupTimeout    time.Duration
	longStopTimeout time.Duration
	testMode        bool // set ONLY by newRuntimeForTest; skips runtimeLoop.
	// nsStatusListeners is a fan-out list of subscribers that receive every
	// NsStatus transition emitted by setStatus. Protected by r.mu; setStatus
	// (writer) runs under Lock; subscribe/unsubscribe helpers also take Lock.
	// Sends are non-blocking — a slow subscriber whose buffer fills drops the
	// event and is expected to re-poll r.Status() on its own timeout.
	nsStatusListeners []chan NsRuntimeStatus
}

// continuation defers a command until a predicate over Runtime state is
// satisfied. predicate MUST be read-only and idempotent. When it returns true,
// cmd is applied INLINE via applyCommand (never enqueued, to preserve ordering
// relative to the current iteration's stepAllApps).
type continuation struct {
	predicate func(*Runtime) bool
	cmd       runtimeCmd
	tag       string
}

// SetRegistryAuthFunc sets the function used to look up registry credentials for image pulls.
// Storage is via atomic.Pointer so concurrent worker goroutines can read without a data race.
func (r *Runtime) SetRegistryAuthFunc(fn RegistryAuthFunc) {
	r.registryAuthFn.Store(&fn)
}

// registryAuth returns credentials for image, or nil if no registryAuthFn is
// set. The double nil check is load-bearing: SetRegistryAuthFunc(nil) stores
// a non-nil pointer to a nil function value (via &fn where fn is nil), so
// Load() would return non-nil but (*fnp)(image) would panic calling nil.
// Happens in production when the workspace config has zero private registries
// (public-only community deploys, for example).
func (r *Runtime) registryAuth(image string) *docker.RegistryAuth {
	fnp := r.registryAuthFn.Load()
	if fnp == nil || *fnp == nil {
		return nil
	}
	return (*fnp)(image)
}

// SetHistory sets the operation history logger.
func (r *Runtime) SetHistory(h *OperationHistory) {
	r.history = h
}

// SetReconcilerConfig overrides default reconciler settings (from daemon.yml).
// Settings are applied to the runtimeLoop's tick()-driven scheduler by updating
// reconcilerInterval, the Enabled / LivenessEnabled gate flags, and the
// per-app LivenessPeriod fallback used when an app omits PeriodSeconds.
//
// Callers are expected to start from DefaultReconcilerConfig() (Enabled=true,
// LivenessEnabled=true) and flip only the fields explicitly set in daemon.yml
// — see internal/daemon/server.go. A zero-value cfg will disable both gates.
func (r *Runtime) SetReconcilerConfig(cfg ReconcilerConfig) {
	r.reconcilerCfg = &cfg
	if cfg.IntervalSeconds > 0 {
		r.reconcilerInterval = time.Duration(cfg.IntervalSeconds) * time.Second
	}
	r.reconcilerEnabled = cfg.Enabled
	r.livenessEnabled = cfg.LivenessEnabled
	// LivenessPeriod is not a single global knob anymore — per-app
	// PeriodSeconds on AppProbeDef drives the schedule. Retained on
	// r.reconcilerCfg as a fallback consumed by periodForProbe when an app
	// omits PeriodSeconds (see runtime_loop.go).
}

// SetPullConcurrency overrides the pull semaphore capacity (from daemon.yml).
// The dispatcher owns the only pull semaphore.
func (r *Runtime) SetPullConcurrency(n int) {
	if n > 0 {
		r.dispatcher.SetPullConcurrency(n)
	}
}

// SetDefaultStopTimeout overrides the default stop timeout (from daemon.yml docker.stopTimeout).
func (r *Runtime) SetDefaultStopTimeout(seconds int) {
	if seconds > 0 {
		r.defaultStopTimeout = seconds
	}
}

// resolveStopTimeout returns the effective stop timeout in seconds: the
// per-app StopTimeout if set, otherwise the runtime's defaultStopTimeout.
// Centralizes the fallback logic used by StopApp / RestartApp / doStart
// stale-sweep / reconciler / beginGroupStopUnderLock.
// Returns 0 when neither appdef nor daemon.yml configure one — Docker
// applies its own 10s default in that case. T23 accounts for this via the
// dockerDefaultStop constant in tickUnderLock.
func (r *Runtime) resolveStopTimeout(appStopTimeout int) int {
	if appStopTimeout > 0 {
		return appStopTimeout
	}
	return r.defaultStopTimeout
}

// ManualStoppedApps returns a copy of manually stopped apps (for generator detached apps).
func (r *Runtime) ManualStoppedApps() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]bool, len(r.manualStoppedApps))
	maps.Copy(result, r.manualStoppedApps)
	return result
}

// WaitForInitialReconcile blocks until the namespace leaves STARTING state
// (transitions to RUNNING, STALLED, or any other state). Returns immediately
// if the namespace is already past STARTING. Respects context cancellation.
//
// Subscribes to nsStatusListeners — setStatus fans out transitions under Lock,
// so this blocks until a non-STARTING value arrives or ctx is canceled. The
// second Status() check after subscribing fences against the race where the
// transition fires between the initial fast-path check and the subscribe call.
func (r *Runtime) WaitForInitialReconcile(ctx context.Context) {
	// Fast-path: already past STARTING, no subscription needed.
	if r.Status() != NsStatusStarting {
		return
	}
	ch := r.subscribeNsStatus()
	defer r.unsubscribeNsStatus(ch)
	// Re-check after subscribing: if the transition fired between the
	// fast-path read and subscribe, the subscriber missed it. Re-read
	// r.Status() to catch that case.
	if r.Status() != NsStatusStarting {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ch:
			if s != NsStatusStarting {
				return
			}
		}
	}
}

// subscribeNsStatus registers a buffered channel that receives every NsStatus
// transition emitted by setStatus. Buffer size 4 is generous — status
// transitions are rare compared to event flush cadence. A slow subscriber that
// fills its buffer loses events silently (non-blocking send); callers must
// treat missed events as "re-poll r.Status() on timeout" and not rely on
// exhaustive delivery. Caller must unsubscribe on exit.
func (r *Runtime) subscribeNsStatus() chan NsRuntimeStatus {
	ch := make(chan NsRuntimeStatus, 4)
	r.mu.Lock()
	r.nsStatusListeners = append(r.nsStatusListeners, ch)
	r.mu.Unlock()
	return ch
}

// unsubscribeNsStatus removes a previously-registered listener channel. The
// channel is NOT closed — the subscriber may still be selecting on it with a
// timeout arm, and closing would trigger a spurious wake-up. GC reclaims the
// channel once both ends are unreferenced.
func (r *Runtime) unsubscribeNsStatus(ch chan NsRuntimeStatus) {
	r.mu.Lock()
	// Linear scan is acceptable — NsStatus listener count is expected to be
	// small (WaitForInitialReconcile subscribes briefly, daemon has a handful).
	for i, sub := range r.nsStatusListeners {
		if sub == ch {
			r.nsStatusListeners = append(r.nsStatusListeners[:i], r.nsStatusListeners[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
}

// SetCachedBundle updates the cached bundle definition (persisted for fallback on resolve failures).
func (r *Runtime) SetCachedBundle(def *bundle.Def) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if def != nil && !def.IsEmpty() {
		r.cachedBundle = def
	}
}

// SetManualStoppedApps restores persisted manual stopped apps (called before Start).
// Takes a defensive copy so the caller's map can't be mutated through runtime operations.
func (r *Runtime) SetManualStoppedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manualStoppedApps = maps.Clone(apps)
	if r.manualStoppedApps == nil {
		r.manualStoppedApps = make(map[string]bool)
	}
}

// RestoreEditedApps restores persisted edited app definitions and lock flags.
func (r *Runtime) RestoreEditedApps(edited map[string]appdef.ApplicationDef, locked []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(edited) > 0 {
		r.editedApps = edited
	}
	for _, name := range locked {
		r.editedLockedApps[name] = true
	}
}

// SetAppLocked sets or clears the lock flag for an edited app.
func (r *Runtime) SetAppLocked(appName string, locked bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if locked {
		r.editedLockedApps[appName] = true
	} else {
		delete(r.editedLockedApps, appName)
	}
	// editedLockedApps is durable user intent. Persist inline + clear r.dirty
	// so the loop tail does not redundantly re-persist.
	r.persistState()
	r.dirty.Store(false)
}

// SetDependsOnDetachedApps stores which detached apps trigger regeneration when restarted.
// Takes a defensive copy — the generator's map may be reused.
func (r *Runtime) SetDependsOnDetachedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependsOnDetachedApps = maps.Clone(apps)
}

// NewRuntime creates a new namespace runtime.
func NewRuntime(cfg *Config, dockerClient docker.RuntimeClient, volumesBase string) *Runtime {
	r := &Runtime{
		status:            NsStatusStopped,
		config:            cfg,
		apps:              make(map[string]*AppRuntime),
		docker:            dockerClient,
		nsID:              cfg.ID,
		volumesBase:       volumesBase,
		manualStoppedApps: make(map[string]bool),
		editedApps:        make(map[string]appdef.ApplicationDef),
		editedLockedApps:  make(map[string]bool),
		livenessFailures:  make(map[string]int),
		restartCounts:     make(map[string]int),
		eventCh:           make(chan api.EventDto, 256),

		signalCh:           NewSignalQueue(),
		cmdQueue:           NewCmdQueue(),
		resultCh:           make(chan workers.Result, 128),
		nowFunc:            time.Now,
		shutdownComplete:   make(chan struct{}),
		tickerPeriod:       1 * time.Second,
		statsInterval:      5 * time.Second,
		reconcilerInterval: 60 * time.Second,
		reconcilerEnabled:  true,
		livenessEnabled:    true,
		livenessNextAt:     make(map[string]time.Time),
		// T23 STOPPING budgets — see field doc comment.
		groupTimeout:    defaultGroupTimeout,
		longStopTimeout: defaultLongStopTimeout,
	}
	r.dispatcher = NewDispatcher(context.Background(), &r.wg, defaultPullConcurrency)
	go r.dispatchLoop()
	return r
}

// signalShutdown closes shutdownComplete exactly once. Safe to call from any
// goroutine; subsequent calls are no-ops thanks to the sync.Once guard.
//
// Uses a dedicated signalOnce — distinct from teardownOnce in shutdownAfter —
// so that the cmdStop post-network continuation calling signalShutdown does
// NOT consume the teardown guard. A subsequent Shutdown() must still run the
// full teardown body (drain wg, close eventCh).
func (r *Runtime) signalShutdown() {
	r.signalOnce.Do(func() { close(r.shutdownComplete) })
}

// SetEventCallback registers a callback for namespace and app state change events.
func (r *Runtime) SetEventCallback(cb EventCallback) {
	r.eventCb.Store(&cb)
}

// emitEvent buffers an event for delivery on the next runtimeLoop iteration.
// Must be called with r.mu held (writer-side guard on eventBuffer; see field doc
// comment). flushEvents drains the buffer to eventCh in append order at the end
// of each iteration. signalCh.Flush() wakes the loop within debounce (≤100ms).
func (r *Runtime) emitEvent(evt api.EventDto) {
	r.eventBuffer = append(r.eventBuffer, evt)
	// signalCh is unconditionally initialized in NewRuntime and never nilled,
	// so no nil-guard is needed here.
	r.signalCh.Flush()
}

// dispatchLoop drains eventCh and calls eventCb outside any lock.
func (r *Runtime) dispatchLoop() {
	for evt := range r.eventCh {
		if cb := r.eventCb.Load(); cb != nil {
			(*cb)(evt)
		}
	}
}

// Status returns the current namespace lifecycle status.
func (r *Runtime) Status() NsRuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Apps returns a snapshot of all app states.
func (r *Runtime) Apps() []*AppRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*AppRuntime, 0, len(r.apps))
	for _, app := range r.apps {
		cp := *app
		result = append(result, &cp)
	}
	return result
}

// FindApp returns a copy of the named app, or nil if not found.
// Uses direct map lookup under RLock — O(1) instead of O(n).
func (r *Runtime) FindApp(name string) *AppRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	app, ok := r.apps[name]
	if !ok {
		return nil
	}
	cp := *app
	return &cp
}
