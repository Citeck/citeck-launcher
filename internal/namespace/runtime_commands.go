package namespace

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// Start begins the namespace lifecycle with the given app definitions.
func (r *Runtime) Start(apps []appdef.ApplicationDef) {
	if r.testMode {
		// testMode runtimes are driven exclusively via StepOnce. Spawning
		// runtimeLoop here would race with the test driver. Catching this in
		// production paths defends against accidental flag leakage.
		panic("namespace.Runtime.Start called on a testMode runtime; use StepOnce / RunUntilQuiescent instead")
	}
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
	// zero-downtime detach intent. Retry up to 3× (~1.5s total) before giving
	// up.
	var lastErr error
	for range 3 {
		if err := r.cmdQueue.Enqueue(cmdDetach{}); err != nil {
			lastErr = err
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
		// Wait for all app/reconciler goroutines to finish before closing eventCh.
		// doStop already waits, but this is a belt-and-suspenders guard in case
		// shutdown is called after running was already cleared.
		r.appWg.Wait()
		close(r.eventCh) // stops dispatchLoop
		if r.ownsActions {
			r.actionSvc.Shutdown()
		}
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

// UpdateAppDef updates the ApplicationDef for a running app and marks it as edited.
// Edited apps are persisted and optionally locked to survive regeneration.
func (r *Runtime) UpdateAppDef(appName string, def appdef.ApplicationDef, lock bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	app, ok := r.apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	app.Def = def
	r.editedApps[appName] = def
	if lock {
		r.editedLockedApps[appName] = true
	}
	// editedApps / editedLockedApps are durable user edits. Persist inline +
	// clear r.dirty.
	r.persistState()
	r.dirty.Store(false)
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
		if app.ContainerID == "" {
			// T19c: STARTING in init phase. Cancel the init worker; spare
			// any pre-existing OpStop. Dispatch stopContainer targeting the
			// init container name; on T21 desiredNext="" routes to STOPPED.
			r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
			app.desiredNext = ""
			app.initialSweep = false
			app.stoppingStartedAt = r.nowFunc()
			r.setAppStatus(app, AppStatusStopping)
			initContainerName := r.docker.ContainerName(appName + "-init")
			plan = r.makeStopPlan(appName, initContainerName, stopTimeout)
			dispatchStop = true
		} else {
			// T19 (STARTING-startPhase / probePhase): main container exists.
			r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
			app.desiredNext = ""
			app.initialSweep = false
			app.stoppingStartedAt = r.nowFunc()
			r.setAppStatus(app, AppStatusStopping)
			plan = r.makeStopPlan(appName, containerName, stopTimeout)
			dispatchStop = true
		}
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
		// STOPPING → T21 → READY_TO_PULL → start. The new dispatch supersedes
		// any prior canceled stop via the dispatcher (attemptID bump).
		delete(r.manualStoppedApps, appName)
		app.desiredNext = AppStatusReadyToPull
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		r.setAppStatus(app, AppStatusStopping)
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
//   - For STOPPING: set desiredNext=READY_TO_PULL so T21 routes to READY_TO_PULL
//     instead of STOPPED.
//
// Emits exactly one restart_event{reason:"user_restart"} via emitRestartEvent,
// the sole write path for restart_event.
func (r *Runtime) RestartApp(appName string) error { //nolint:gocyclo // single-pass dispatch over the per-status restart branches
	// Capture r.runCtx once under the initial Lock. Never re-read r.runCtx —
	// doStop may concurrently swap it to nil, and a double-read could see
	// "running" then dispatch against a nil context. The captured variable is
	// stable for the lifetime of this call.
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
	runCtx := r.runCtx
	r.mu.Unlock()

	if runCtx == nil {
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
		// bump).
		r.dispatcher.CancelApp(appName, workers.CancelStopApp, workers.OpStop)
		app.desiredNext = AppStatusReadyToPull
		app.initialSweep = false
		app.stoppingStartedAt = r.nowFunc()
		r.emitRestartEvent(app, "user_restart", "", "")
		r.incrementRestartCount(appName)
		containerID := app.ContainerID
		r.setAppStatus(app, AppStatusStopping)
		// RestartApp clears manualStoppedApps (re-attach) and appends a
		// user_restart event — durable intent. Persist inline + clear r.dirty.
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
			if app, ok = r.apps[appName]; ok && app.Status == AppStatusStopping {
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
		// Direct re-entry to READY_TO_PULL — no container in flight.
		r.emitRestartEvent(app, "user_restart", "", "")
		r.incrementRestartCount(appName)
		r.setAppStatus(app, AppStatusReadyToPull)
		// Durable detach-clear + restart event. Persist inline + clear r.dirty.
		r.persistState()
		r.dirty.Store(false)
		r.mu.Unlock()
	case AppStatusPulling, AppStatusReadyToStart, AppStatusDepsWaiting:
		// Already on the path; no-op. No restart_event emitted — the user's
		// intent is already being satisfied by the in-flight progression.
		r.mu.Unlock()
	case AppStatusStopping:
		// Stop already in flight; set desiredNext so T21 routes to
		// READY_TO_PULL on completion. Emit a user_restart event so observers
		// can distinguish this caller's intent from any prior StopApp/
		// RestartApp that drove the app into STOPPING.
		r.emitRestartEvent(app, "user_restart", "", "")
		r.incrementRestartCount(appName)
		app.desiredNext = AppStatusReadyToPull
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
