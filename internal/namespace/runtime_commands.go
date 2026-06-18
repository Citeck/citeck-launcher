package namespace

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// Start begins the namespace lifecycle with the given app definitions. Images
// are pulled per the normal stage rules (snapshot tags pulled; present release
// tags reused). A "force update and start" forces a git pull of the workspace /
// bundle repos (so new bundle versions are picked up) and then starts via this
// same path — image pulling stays normal, matching Kotlin 1.x where forceUpdate
// only flips the git policy to REQUIRED, never the image pull policy.
func (r *Runtime) Start(apps []appdef.ApplicationDef) {
	if r.testMode {
		// testMode runtimes are driven exclusively via StepOnce. Spawning
		// runtimeLoop here would race with the test driver. Catching this in
		// production paths defends against accidental flag leakage.
		panic("namespace.Runtime.Start called on a testMode runtime; use StepOnce / RunUntilQuiescent instead")
	}
	// A just-completed stop clears r.running only when the runtimeLoop goroutine
	// returns (a defer), which lags the STOPPED status by a full loop-tail
	// iteration (doStop publishes NsStatusStopped, then signalShutdown closes
	// shutdownComplete, then the loop runs one more tail and only THEN returns →
	// running=false). A caller that synchronizes on STOPPED and immediately calls
	// Start() can land in that window, where the guard below would see
	// running==true and silently drop the restart. Wait for the previous loop to
	// finish first so a stop→start sequence is never lost.
	r.awaitStoppedLoopExit()
	if !r.running.CompareAndSwap(false, true) {
		slog.Warn("Runtime already running, ignoring Start()")
		return
	}
	// Re-initialize shutdown-signal primitives. A previous cmdStop may have
	// closed shutdownComplete via signalShutdown; sync.Once can't re-fire,
	// so a new runtimeLoop would observe the already-closed channel on its
	// first select and exit immediately. Also reset detaching — a previous
	// detach may have set it.
	//
	// Scope: this supports Start-after-Stop (stop namespace, then start it
	// again in the same daemon process). It does NOT support Start-after-
	// Shutdown — Shutdown closes eventCh via teardownOnce, so a subsequent
	// Start would panic on send-to-closed-channel. Daemon lifecycle expects
	// one Shutdown per process.
	r.mu.Lock()
	r.shutdownComplete = make(chan struct{})
	r.signalOnce = sync.Once{}
	r.detaching.Store(false)
	// Drop any STOPPED definitions retained from the prior run (doStop keeps
	// them so per-app config/file editing works while stopped) so the restarted
	// loop starts from a clean slate; the cmdStart below (doStart) rebuilds
	// r.apps from the desired set before the first stepAllApps.
	r.apps = make(map[string]*AppRuntime)
	r.mu.Unlock()

	r.wg.Add(1)
	go r.runtimeLoop()
	if err := r.cmdQueue.Enqueue(cmdStart{apps: apps}); err != nil {
		slog.Error("Failed to enqueue cmdStart", "err", err)
	}
}

// Stop signals the runtime to begin shutting down.
func (r *Runtime) Stop() {
	if err := r.cmdQueue.Enqueue(cmdStop{}); err != nil {
		slog.Error("Failed to enqueue cmdStop", "err", err)
	}
}

// Stop→start race-window tuning. r.running is cleared by a defer when the
// runtimeLoop goroutine returns, lagging the published STOPPED status;
// awaitStoppedLoopExit waits out that gap before a restart.
const (
	loopExitWaitTimeout  = 5 * time.Second
	loopExitPollInterval = time.Millisecond
)

// awaitStoppedLoopExit blocks until a previous runtimeLoop that is winding down
// has fully exited (running==false), so Start() does not race the loop's
// running=false defer. It waits ONLY when a stop/detach has actually been
// signaled (shutdownComplete is closed); on a fresh or genuinely-running
// runtime shutdownComplete is open and it returns immediately. The wait is
// bounded — on timeout Start's CompareAndSwap falls back to the historical
// "ignoring Start()" behavior rather than blocking forever.
func (r *Runtime) awaitStoppedLoopExit() {
	r.mu.RLock()
	sc := r.shutdownComplete
	r.mu.RUnlock()
	select {
	case <-sc:
		// A stop/detach was signaled — the loop is exiting (or has exited).
	default:
		return // not stopping: genuinely running, or never started
	}
	deadline := time.Now().Add(loopExitWaitTimeout)
	for r.running.Load() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(loopExitPollInterval)
	}
}

// Detach signals the runtime to exit without stopping containers.
// Used for binary upgrades: the daemon process exits but the platform
// keeps running, and the next daemon attaches to existing containers
// via doStart's hash-matching path.
//
// Returns false if a stop is already in flight (status STOPPING) — in
// that case the runtime will fall through doStop's container-stopping
// path and detach is no longer possible. Callers should check the
// return value and surface a clear error to the user. Also returns false
// if the cmdQueue enqueue fails (ErrCmdQueueFull) so callers can retry.
func (r *Runtime) Detach() bool {
	r.mu.RLock()
	stopInFlight := r.status == NsStatusStopping
	r.mu.RUnlock()
	if stopInFlight {
		return false
	}
	// cmdDetach is terminal; tolerate a slower enqueue than the default 500ms.
	// A backpressured queue at shutdown is rare (cap=256, no realistic burst)
	// but forcing fallback to Stop() via shutdownAfter would defeat the
	// zero-downtime detach intent. Retry up to 3× with a short backoff between
	// attempts — each Enqueue already has its own 500ms timeout on the queue
	// channel, so total worst case is ~1.9s before we give up.
	var lastErr error
	for i := range 3 {
		if err := r.cmdQueue.Enqueue(cmdDetach{}); err != nil {
			lastErr = err
			if i < 2 {
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		return true
	}
	slog.Error("Failed to enqueue cmdDetach after retries; caller will fall back to Stop()", "err", lastErr)
	return false
}

// Shutdown stops the runtime and waits for all goroutines to complete.
func (r *Runtime) Shutdown() {
	r.shutdownAfter(false)
}

// ShutdownDetached exits the runtime without stopping containers, then
// waits for all goroutines to complete. Use for binary upgrades.
//
// Best-effort: if Stop()/Shutdown() is already in flight (runtime status
// is STOPPING), containers will still be stopped and ShutdownDetached
// degrades into a regular shutdown wait. The first caller into
// teardownOnce wins, so concurrent Shutdown/ShutdownDetached invocations
// produce a single teardown — whichever path that turns out to be.
func (r *Runtime) ShutdownDetached() {
	r.shutdownAfter(true)
}

// shutdownAfter is the shared one-shot teardown path. When leaveRunning is
// true, the runtime exits without touching containers; otherwise it stops
// them gracefully (legacy Shutdown semantics). If leaveRunning is requested
// but a stop is already in flight, the function silently degrades to
// waiting on the existing stop (containers will be stopped).
func (r *Runtime) shutdownAfter(leaveRunning bool) {
	r.teardownOnce.Do(func() {
		// r.running is flipped false by a defer in runtimeLoop that fires
		// AFTER runtimeLoop's own r.wg.Done() (defers run LIFO). So by the
		// time r.running.Load() returns false here, the loop's wg contribution
		// is already decremented — but dispatcher workers (which share the
		// same WaitGroup) may still be live. The r.wg.Wait() below still
		// drains them. teardownOnce prevents a double-entry from racing.
		if r.running.Load() {
			signaled := false
			if leaveRunning {
				signaled = r.Detach()
			}
			if !signaled {
				r.Stop()
			}
			r.wg.Wait()
		}
		close(r.eventCh)            // stops dispatchLoop
		fakeClockBindings.delete(r) // no-op for production runtimes (not in map)
	})
}

// Regenerate sends a regeneration command to the runtime loop.
// If cfg is non-nil, the runtime config is atomically updated before regenerating.
// If bundleDef is non-nil, it is persisted as the cached bundle for fallback on future resolve failures.
func (r *Runtime) Regenerate(apps []appdef.ApplicationDef, cfg *Config, bundleDef *bundle.Def) {
	if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: apps, cfg: cfg, bundleDef: bundleDef}); err != nil {
		slog.Error("Failed to enqueue cmdRegenerate", "err", err)
	}
}

// generatedDefForApp returns the last freshly-generated def for appName (the
// baseline a patch is computed against). Must be called with r.mu held.
func (r *Runtime) generatedDefForApp(appName string) (appdef.ApplicationDef, bool) {
	// Prefer the running desired set (post-start, authoritative); fall back to
	// the load-time generated set so config view/edit works while stopped.
	for _, d := range r.lastApps {
		if d.Name == appName {
			return d, true
		}
	}
	if d, ok := r.generatedDefs[appName]; ok {
		return d, true
	}
	return appdef.ApplicationDef{}, false
}

// UpdateAppDef stores the user-edited def as a delta over the generated
// baseline. `lock` is retained for API compatibility (a stored patch is
// inherently sticky — always re-applied on regen).
func (r *Runtime) UpdateAppDef(appName string, def appdef.ApplicationDef, lock bool) error {
	_ = lock
	r.mu.Lock()
	defer r.mu.Unlock()
	app, appLive := r.apps[appName]
	base, hasBase := r.generatedDefForApp(appName)
	if !appLive && !hasBase {
		// Neither a live app nor a generated baseline → unknown app.
		return fmt.Errorf("app %q not found", appName)
	}
	if !hasBase {
		base = app.Def
	}
	patch, err := DiffAppDef(base, def)
	if err != nil {
		return fmt.Errorf("compute app patch: %w", err)
	}
	if patch == nil {
		delete(r.editedAppPatches, appName)
	} else {
		r.editedAppPatches[appName] = patch
	}
	if appLive {
		app.Def = def
	}
	// editedAppPatches is a durable user edit. Persist inline + clear r.dirty.
	r.persistState()
	r.dirty.Store(false)
	if len(r.lastApps) > 0 {
		if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: r.lastApps}); err != nil {
			slog.Warn("Failed to enqueue regenerate after UpdateAppDef", "app", appName, "err", err)
		}
	}
	return nil
}

// ResetAppDef removes the user-edited ApplicationDef override for `appName`
// so the next regeneration restores the generated default. Mirrors Kotlin's
// `NamespaceRuntime.resetAppDef` (used by AppCfgEditWindow's Reset button).
//
// Returns nil even if the app was not edited — idempotent, matches Kotlin.
func (r *Runtime) ResetAppDef(appName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, live := r.apps[appName]; !live {
		if _, hasBase := r.generatedDefForApp(appName); !hasBase {
			return fmt.Errorf("app %q not found", appName)
		}
	}
	delete(r.editedAppPatches, appName)
	r.persistState()
	r.dirty.Store(false)
	// Trigger a regeneration so the original ApplicationDef is re-installed
	// on the running runtime; without this the user would have to manually
	// reload the namespace to see the reset take effect. Pass r.lastApps —
	// an empty cmdRegenerate{} wipes r.apps because doRegenerate treats
	// apps=nil as "every app removed from the desired set".
	if len(r.lastApps) > 0 {
		if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: r.lastApps}); err != nil {
			slog.Warn("Failed to enqueue regenerate after ResetAppDef", "app", appName, "err", err)
		}
	}
	return nil
}

// WriteEditedFile records a user file edit as a delta over `template` (the
// generated content for this key) and atomically writes the edited content to
// disk under a single r.mu hold. Computing the delta before locking keeps the
// (parse-heavy) merge work out of the critical section.
//
// absPath MUST resolve under volumesBase (the caller validates this); we
// re-use the existing fsutil.AtomicWriteFile here so a crash mid-write
// leaves the previous file intact.
func (r *Runtime) WriteEditedFile(relPath, absPath string, content, template []byte) error {
	base := relPath[strings.LastIndex(relPath, "/")+1:]
	edit, err := MakeFileEdit(base, template, content)
	if err != nil {
		return fmt.Errorf("compute file edit: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := fsutil.AtomicWriteFile(absPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", absPath, err)
	}
	r.editedFileEdits[relPath] = edit
	r.persistState()
	r.dirty.Store(false)
	// MUST pass the current desired set: an empty cmdRegenerate{} wipes
	// r.apps because doRegenerate treats apps=nil as "every app removed from
	// the desired set" (same gotcha as ResetAppDef / ResetEditedFile). Skip
	// the regenerate when the runtime hasn't started yet (the on-disk file is
	// enough; the next Start picks it up via VolumesContentHash).
	if len(r.lastApps) > 0 {
		if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: r.lastApps}); err != nil {
			slog.Warn("Failed to enqueue regenerate after WriteEditedFile", "path", relPath, "err", err)
		}
	}
	return nil
}

// IsFileEdited reports whether relPath ("<app>/<rel-path>", no leading "./")
// has been marked as user-edited.
func (r *Runtime) IsFileEdited(relPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.editedFileEdits[relPath]
	return ok
}

// editedFilesForAppLocked returns the user-edited mounted-file paths that
// belong to appName. Ownership is decided by the app's actual bind-mount host
// paths — NOT a naive "appName/" prefix: webapp mounts live under
// "./app/<name>/props/…" (host key "app/<name>/props"), so the first path
// segment is "app", never the app name. Result is a fresh slice; safe to
// mutate by the caller. Callers MUST hold r.mu (read or write) — used by
// ToNamespaceDto under RLock for every app DTO, so the helper deliberately
// skips its own lock acquisition to avoid per-app RLock churn.
func (r *Runtime) editedFilesForAppLocked(appName string) []string {
	if len(r.editedFileEdits) == 0 {
		return nil
	}
	def, ok := r.generatedDefForApp(appName)
	if !ok {
		if app, live := r.apps[appName]; live {
			def, ok = app.Def, true
		}
	}
	if !ok {
		return nil
	}
	hostKeys := collectFileKeysFromVolumes(def.Volumes)
	if len(hostKeys) == 0 {
		return nil
	}
	var out []string
	for path := range r.editedFileEdits {
		for _, key := range hostKeys {
			// File mount: exact match. Directory mount: any file beneath it.
			if path == key || strings.HasPrefix(path, key+"/") {
				out = append(out, path)
				break
			}
		}
	}
	return out
}

// ResetEditedFile clears the user-edit flag for a single mounted bind-mount
// file. Mirrors ResetAppDef: validates that appName refers to a known app,
// removes the in-memory flag, persists state, and enqueues a regenerate so
// the original generator-supplied content is materialized back on disk by
// the next writeRuntimeFiles call.
//
// relPath MUST be the canonical "<app>/<rel-path>" key with NO leading "./".
// Returns nil even if the file was not previously edited — idempotent,
// matches the ResetAppDef contract.
func (r *Runtime) ResetEditedFile(appName, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, live := r.apps[appName]; !live {
		if _, hasBase := r.generatedDefForApp(appName); !hasBase {
			return fmt.Errorf("app %q not found", appName)
		}
	}
	delete(r.editedFileEdits, relPath)
	r.persistState()
	r.dirty.Store(false)
	// Trigger a regeneration so the original file content is written back to
	// disk on the next writeRuntimeFiles. MUST pass r.lastApps — an empty
	// cmdRegenerate{} makes doRegenerate treat every existing app as
	// removed-from-desired-set and wipe r.apps (same gotcha as
	// WriteEditedFile / ResetAppDef).
	if len(r.lastApps) > 0 {
		if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: r.lastApps}); err != nil {
			slog.Warn("Failed to enqueue regenerate after ResetEditedFile", "app", appName, "path", relPath, "err", err)
		}
	}
	return nil
}

// StopApp stops a single app by name and marks it as detached.
// Detached apps are excluded from namespace start/reload/regenerate.
// Use StartApp to re-attach.
//
// Routes through the state machine via T19 / T19b / T19c:
//   - T19  (RUNNING / STARTING-startPhase / STARTING-probePhase / FAILED /
//     START_FAILED / STOPPING_FAILED): set desiredNext="" + transition
//     to STOPPING + dispatch stopContainer for the main container.
//   - T19b (PULLING / READY_TO_PULL / READY_TO_START / DEPS_WAITING /
//     PULL_FAILED): no container exists yet → cancel any pull/start
//     workers and mark STOPPED directly.
//   - T19c (STARTING-initPhase, detected via app.Status==STARTING &&
//     app.ContainerID==""): cancel the in-flight init worker, dispatch
//     stopContainer on "{appName}-init" (init container artifact).
//
// manualStoppedApps[appName]=true is set BEFORE dispatch so the user's
// detach intent persists even if the stop fails.
func (r *Runtime) StopApp(appName string) error { //nolint:gocyclo // single-pass dispatch over T19/T19b/T19c branches
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}

	// Mark as detached immediately — the user's intent to detach must be
	// recorded even if the Docker stop fails (container already gone, etc.).
	r.manualStoppedApps[appName] = true

	containerName := r.docker.ContainerName(appName)
	stopTimeout := r.resolveStopTimeout(app.Def.StopTimeout)

	var plan dispatchPlan
	dispatchStop := false

	switch app.Status {
	case AppStatusReadyToPull, AppStatusPulling, AppStatusPullFailed,
		AppStatusReadyToStart, AppStatusDepsWaiting:
		// T19b: no container exists. Cancel any in-flight pull or start
		// worker (StopApp reason — applyWorkerResult drops canceled Results
		// silently because the source-state guard no longer matches STOPPED).
		r.dispatcher.CancelApp(appName, workers.CancelStopApp)
		app.ContainerID = ""
		r.setAppStatus(app, AppStatusStopped)
	case AppStatusStarting:
		// T19/T19c: cancel the init worker, then stop BOTH the main and a
		// possibly-orphaned "<app>-init" container. We must not branch on
		// ContainerID: the start worker can have already created the main
		// container while its ContainerID Result is not yet applied
		// (app.ContainerID == ""), and targeting only "<app>-init" there leaked
		// the running main container (and stranded its host ports).
		r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
		app.desiredNext = ""
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		r.setAppStatus(app, AppStatusStopping)
		plan = r.makeStartingStopPlan(appName, stopTimeout)
		dispatchStop = true
	case AppStatusRunning, AppStatusFailed, AppStatusStartFailed, AppStatusStoppingFailed:
		// T19: container exists (or is presumed to). Dispatch stop and
		// transition to STOPPING.
		r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
		app.desiredNext = ""
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		r.setAppStatus(app, AppStatusStopping)
		plan = r.makeStopPlan(appName, containerName, stopTimeout)
		dispatchStop = true
	case AppStatusStopping:
		// Already stopping — no-op other than recording detach intent
		// (which we already did above).
	case AppStatusUpdating:
		// Runtime-driven recreate is in flight. The user pressed Stop —
		// promote it to a real stop: drop desiredNext (don't let T21 route
		// to READY_TO_PULL after the stopContainer Result lands) and flip
		// the status to STOPPING so handleStopResult routes to STOPPED
		// rather than continuing the recreate.
		app.desiredNext = ""
		app.initialSweep = false
		r.setAppStatus(app, AppStatusStopping)
	case AppStatusStopped:
		// Already stopped — record intent only.
	}

	// StopApp records durable detach intent (manualStoppedApps) that must
	// survive a crash. Persist inline and clear r.dirty so the loop tail does
	// not redundantly re-persist the same state.
	r.persistState()
	r.dirty.Store(false)
	r.mu.Unlock()

	if dispatchStop {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
	r.signalCh.Flush()
	return nil
}

// StartApp starts a single app that is not currently running.
// Unlike RestartApp, it handles never-started apps (READY_TO_PULL,
// PULL_FAILED, START_FAILED).
//
// Routes through the state machine via T27 / T28 / T29 / T30:
//   - T27 (STOPPED detached): clear manualStoppedApps + READY_TO_PULL.
//   - T28 (FAILED): reset retry + READY_TO_PULL.
//   - T29 (START_FAILED): reset retry + READY_TO_START (image already pulled).
//   - T30 (STOPPING_FAILED): fresh STOPPING attempt with
//     desiredNext=READY_TO_PULL; T21 routes back through READY_TO_PULL → start.
//
// Other source states fall through to RestartApp.
func (r *Runtime) StartApp(appName string) error {
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}

	var plan dispatchPlan
	dispatchStop := false

	switch app.Status {
	case AppStatusRunning:
		r.mu.Unlock()
		return nil // already running
	case AppStatusStopped:
		// T27: detached → re-attach. Clear detach flag, transition to
		// READY_TO_PULL. State machine drives pull/start.
		delete(r.manualStoppedApps, appName)
		r.resetRetry(appName)
		r.setAppStatus(app, AppStatusReadyToPull)
	case AppStatusReadyToPull, AppStatusPullFailed, AppStatusFailed:
		// T28 (FAILED) — also handles READY_TO_PULL / PULL_FAILED for
		// idempotency: clear retry and let the state machine pick it up.
		delete(r.manualStoppedApps, appName)
		r.resetRetry(appName)
		r.setAppStatus(app, AppStatusReadyToPull)
	case AppStatusStartFailed:
		// T29: image is already pulled — go straight to READY_TO_START.
		delete(r.manualStoppedApps, appName)
		r.resetRetry(appName)
		r.setAppStatus(app, AppStatusReadyToStart)
	case AppStatusStoppingFailed:
		// T30: fresh stop attempt. desiredNext=READY_TO_PULL routes through
		// UPDATING → T21 → READY_TO_PULL → start. The new dispatch supersedes
		// any prior canceled stop via the dispatcher (attemptID bump).
		// UPDATING (not STOPPING) marks this as recreate-in-flight.
		delete(r.manualStoppedApps, appName)
		app.desiredNext = AppStatusReadyToPull
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		r.setAppStatus(app, AppStatusUpdating)
		stopTimeout := r.resolveStopTimeout(app.Def.StopTimeout)
		containerName := r.docker.ContainerName(appName)
		plan = r.makeStopPlan(appName, containerName, stopTimeout)
		dispatchStop = true
	default:
		r.mu.Unlock()
		// For other states (PULLING, STARTING, STOPPING, etc.), delegate to
		// RestartApp — full container teardown + restart.
		return r.RestartApp(appName)
	}

	// Persist inline + clear r.dirty to avoid a redundant tail write.
	r.persistState()
	r.dirty.Store(false)
	r.mu.Unlock()

	if dispatchStop {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
	r.signalCh.Flush()
	return nil
}

// RetryPullFailedApps re-queues all apps in PULL_FAILED state for pull+start.
// Called after secrets change so that apps that failed due to missing auth
// can recover.
//
// T26: transition each PULL_FAILED app to READY_TO_PULL and reset retry state
// so T24's backoff doesn't gate the next attempt. The state machine drives the
// rest via T2 → T5 → T7 → … on the next stepAllApps iteration.
func (r *Runtime) RetryPullFailedApps() int {
	r.mu.Lock()
	if r.runCtx == nil {
		// Early-return if runtime not yet started — avoids spurious signalCh wakes during setup/Shutdown.
		r.mu.Unlock()
		return 0
	}
	var retried int
	for _, app := range r.apps {
		if app.Status != AppStatusPullFailed {
			continue
		}
		// Explicit retry (e.g. after the user saved registry credentials):
		// clear the auth block so the paused pull is attempted again.
		delete(r.pullAuthBlockedApps, app.Name)
		r.resetRetry(app.Name)
		app.StatusText = ""
		r.setAppStatus(app, AppStatusReadyToPull)
		retried++
	}
	r.mu.Unlock()
	if retried > 0 {
		r.signalCh.Flush()
	}
	return retried
}

// RestartApp stops and re-starts a single app.
//
// Routes through the state machine:
//   - If this app is a dependency of any detached app, fall back to
//     cmdRegenerate (preserves the ACME + proxy-restart flow).
//   - For states with a live/presumed container (RUNNING / STARTING / FAILED /
//     START_FAILED / STOPPING_FAILED): set desiredNext=READY_TO_PULL +
//     transition STOPPING + dispatch stopContainer. T21 applies desiredNext
//     and the state machine walks READY_TO_PULL → T2/T3 → … → RUNNING.
//   - For container-less source states (STOPPED / READY_TO_PULL / PULL_FAILED):
//     direct re-entry at READY_TO_PULL.
//   - For already-in-progress states (PULLING / READY_TO_START / DEPS_WAITING):
//     no-op — the app is already on the path.
//   - For STOPPING: set desiredNext=READY_TO_PULL so T21
//     routes to READY_TO_PULL instead of STOPPED.
//
// Restart counter is bumped but NO restart_event is emitted: the
// "Перезапуски" tab is reserved for non-user causes (OOM, liveness,
// stop-failed, pull-failed retries, …) — explicit user restarts would
// just pollute the log with rows the user already knows about. Reason
// `user_restart` is consequently no longer produced anywhere.
func (r *Runtime) RestartApp(appName string) error { //nolint:gocyclo // single-pass dispatch over the per-status restart branches
	r.mu.Lock()
	if _, ok := r.apps[appName]; !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}
	// Guard against a race with in-flight namespace shutdown: the cmdStop
	// continuation chain relies on apps in each group reaching a terminal stop
	// state (STOPPED or STOPPING_FAILED) for allAppsTerminalInGroup to fire.
	// A RestartApp during NS STOPPING would set desiredNext=READY_TO_PULL on
	// the STOPPING branch — T21 would then route the app back through
	// READY_TO_PULL instead of STOPPED, so the group predicate never becomes
	// true, cmdStopNextGroup never advances, RemoveNetwork never dispatches,
	// and Shutdown's r.wg.Wait() deadlocks. Reject with a clear error; the
	// operator should wait for stop to complete and then call Start.
	if r.status == NsStatusStopping {
		r.mu.Unlock()
		return fmt.Errorf("runtime is stopping, cannot restart app %q", appName)
	}
	needsRegen := r.dependsOnDetachedApps[appName]
	lastApps := r.lastApps
	started := r.runCtx != nil
	r.mu.Unlock()

	if !started {
		return fmt.Errorf("runtime not started, cannot restart app %q", appName)
	}

	// Dependency-of-detached path: regenerate so proxy / ACME / etc. pick the
	// dependency back up. This preserves parity with the legacy pre-4d path.
	if needsRegen {
		slog.Info("Restarting detached-dep app triggers regeneration", "app", appName)
		if err := r.cmdQueue.Enqueue(cmdRegenerate{apps: lastApps}); err != nil {
			slog.Warn("Regenerate (restart) command dropped", "app", appName, "err", err)
			return fmt.Errorf("regenerate enqueue failed: %w", err)
		}
		return nil
	}

	r.mu.Lock()
	// Re-check the NS STOPPING guard: between the initial check + Unlock above
	// and this second Lock, the state machine may have applied cmdStop and
	// flipped r.status to STOPPING. Without this recheck, the switch below
	// could set desiredNext=READY_TO_PULL on a stopping app and deadlock
	// cmdStopNextGroup exactly like the first guard was meant to prevent.
	if r.status == NsStatusStopping {
		r.mu.Unlock()
		return fmt.Errorf("runtime is stopping, cannot restart app %q", appName)
	}
	// Re-lookup under Lock in case StopApp / RegenerateApp raced between the
	// snapshot read above and this point.
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return nil // app vanished — silent no-op (parity with StopApp)
	}
	// Clear detach intent + retry bookkeeping — the user is explicitly asking
	// for a fresh attempt.
	delete(r.manualStoppedApps, appName)
	r.resetRetry(appName)

	containerName := r.docker.ContainerName(appName)
	stopTimeout := r.resolveStopTimeout(app.Def.StopTimeout)

	var plan dispatchPlan
	dispatchStop := false

	switch app.Status {
	case AppStatusRunning, AppStatusStarting, AppStatusFailed, AppStatusStartFailed, AppStatusStoppingFailed:
		// desiredNext=READY_TO_PULL so T21 routes back to the pull-side entry
		// point. Cancel in-flight OpStart / OpInit / OpProbe workers; an
		// earlier OpStop dispatch is superseded by the new Dispatch (attemptID
		// bump). UPDATING (not STOPPING) marks the user-restart recreate so
		// the daemon log distinguishes it from a final stop.
		r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
		app.desiredNext = AppStatusReadyToPull
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		containerID := app.ContainerID
		r.setAppStatus(app, AppStatusUpdating)
		// RestartApp clears manualStoppedApps (re-attach) — durable intent.
		// Persist inline + clear r.dirty. A user restart (incl. applying edited
		// config to a running app) is deliberate, not an abnormal/unscheduled
		// restart, so it emits NO restart_event AND does NOT bump the restart
		// counter (the red "↻N" badge tracks crash/oom/liveness restarts only).
		r.persistState()
		r.dirty.Store(false)
		r.mu.Unlock()
		if containerID != "" {
			plan = r.makeStopPlan(appName, containerName, stopTimeout)
			dispatchStop = true
		} else {
			// STARTING (init phase) with no main container yet. The init
			// worker was just canceled; there is nothing to stop, so skip the
			// stopContainer dispatch and manually advance desiredNext.
			r.mu.Lock()
			if app, ok = r.apps[appName]; ok && app.Status == AppStatusUpdating {
				app.ContainerID = ""
				next := app.desiredNext
				app.desiredNext = ""
				if next == "" {
					next = AppStatusReadyToPull
				}
				r.setAppStatus(app, next)
			}
			r.mu.Unlock()
		}
	case AppStatusStopped, AppStatusReadyToPull, AppStatusPullFailed:
		// Direct re-entry to READY_TO_PULL — no container in flight. A user
		// restart is deliberate: no restart_event and no restart-counter bump.
		r.setAppStatus(app, AppStatusReadyToPull)
		// Durable detach-clear. Persist inline + clear r.dirty.
		r.persistState()
		r.dirty.Store(false)
		r.mu.Unlock()
	case AppStatusPulling, AppStatusReadyToStart, AppStatusDepsWaiting:
		// Already on the path; no-op. No restart_event emitted — the user's
		// intent is already being satisfied by the in-flight progression.
		r.mu.Unlock()
	case AppStatusStopping, AppStatusUpdating:
		// Stop or update already in flight; set desiredNext so T21 routes to
		// READY_TO_PULL on completion. STOPPING came from a user stop the
		// caller now wants to flip into a restart — promote the status to
		// UPDATING so the daemon log reflects the new intent. UPDATING was
		// already on a recreate path; leave it alone. A user restart is
		// deliberate: no restart_event and no restart-counter bump.
		app.desiredNext = AppStatusReadyToPull
		if app.Status == AppStatusStopping {
			r.setAppStatus(app, AppStatusUpdating)
		}
		r.mu.Unlock()
	default:
		r.mu.Unlock()
	}

	if dispatchStop {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
	r.signalCh.Flush()
	return nil
}
