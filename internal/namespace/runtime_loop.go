// Package namespace — runtimeLoop and per-iteration housekeeping.
//
// This file holds runtimeLoop and the helpers it invokes after every select arm:
//   - stepAllApps           (T1–T33 transitions)
//   - evaluateContinuations (continuation primitive; fires pending continuations)
//   - updateNsStatus        (NS status re-derivation under r.mu)
//   - flushEvents           (eventBuffer → eventCh in append order)
//
// tick() splits state mutation (under Lock) from worker dispatch (without
// Lock). Stats dispatch is rate-limited to statsInterval (default 5s).
// Public API methods enqueue typed commands; runtimeLoop's select has a single
// command arm that Drain-coalesces and routes each surviving command to
// applyCommand.
package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// T23 default STOPPING budgets.
//
//   - defaultGroupTimeout is the operator-initiated stop budget. Webapps
//     typically honor SIGTERM in < 10s.
//   - defaultLongStopTimeout is the runtime-initiated recreate budget
//     (stale-cleanup + changed-hash). Java webapps routinely take 30–45s;
//     60s is roomy for normal Java shutdown but surfaces a wedged container
//     within a minute.
//
// Tests that need tighter budgets assign to r.groupTimeout / r.longStopTimeout
// directly on a testMode runtime.
const (
	defaultGroupTimeout    = 10 * time.Second
	defaultLongStopTimeout = 60 * time.Second
)

// runtimeLoop is the single-threaded state-machine driver. It runs a select
// loop with per-iteration stepAllApps / evaluateContinuations /
// updateNsStatus / flushEvents.
func (r *Runtime) runtimeLoop() {
	defer r.wg.Done()
	defer r.running.Store(false)
	slog.Info("Namespace runtime thread started", "namespace", r.nsID)

	// Loop-owned context: canceled on function return so any DrainBurst /
	// tick / stepAllApps call chain that takes ctx observes shutdown
	// promptly. Workers still derive their contexts from the dispatcher's
	// own parentCtx and are unaffected — explicit cancellation still flows
	// through dispatcher.CancelAll / CancelApp.
	loopCtx, loopCancel := context.WithCancel(context.Background())
	defer loopCancel()

	ticker := time.NewTicker(r.tickerPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-r.shutdownComplete:
			// Drain any events buffered by caller-side setStatus before the
			// loop exits — otherwise observers miss the final transitions.
			r.flushEvents()
			return
		case cmd := <-r.cmdQueue.Chan():
			// Drain coalesces adjacent buffered commands, then applies each
			// survivor inline via applyCommand. cmdStop / cmdDetach terminate
			// via shutdownComplete closure; the loop exits on the next iteration.
			r.cmdQueue.Drain(cmd, r.applyCommand)
		case res := <-r.resultCh:
			r.applyWorkerResult(res)
		case <-r.signalCh.C():
			r.signalCh.DrainBurst(loopCtx, 250*time.Millisecond, 4)
		case <-ticker.C:
			r.tick(loopCtx)
		}
		r.stepAllApps()
		r.evaluateContinuations()
		r.updateNsStatus()
		r.flushEvents()
		// Coalesce per-iteration state mutations into a single persistState.
		// Mutators flip r.dirty under Lock; the tail below drains once per
		// iteration. Mutators that record durable user intent (StopApp /
		// StartApp / UpdateAppDef / SetAppLocked / doDetach / RestartApp)
		// persist inline AND clear dirty to skip this redundant write.
		if r.dirty.Load() {
			r.mu.Lock()
			r.persistState()
			r.dirty.Store(false)
			r.mu.Unlock()
		}
	}
}

// stepAllApps walks every non-detached app and applies the per-app state
// machine transitions (T1–T33).
//
// T1 ("adopt existing running container by hash match") is handled only in
// doStart (lock phase). stepAllApps never upgrades READY_TO_PULL → RUNNING via
// adoption; subsequent READY_TO_PULL entries always flow through T2 or T3.
// T4 ("adopt after pull") is not implemented: all successful pulls route
// through T5. doRegenerate reuses containers whose hash matches via
// buildExistingContainerMap before they enter the state machine, so the
// state-machine-level adoption path is unnecessary in practice.
//
// This MUST run after every select case in runtimeLoop — a transition
// committed in this iteration is visible to dependents in the same iteration.
//
// Lock discipline: transitions are committed under r.mu.Lock(); Docker I/O
// for T3 digest refresh happens outside the lock via the tick() pattern.
func (r *Runtime) stepAllApps() {
	plans := r.stepAllAppsUnderLock()
	for _, p := range plans {
		r.dispatcher.Dispatch(p.taskID, p.fn, r.resultCh, r.signalCh)
	}
}

// stepAllAppsUnderLock commits per-app state transitions and accumulates
// dispatchable worker plans. Runs entirely under r.mu.Lock so the r.apps map
// stays coherent for concurrent RLock readers.
func (r *Runtime) stepAllAppsUnderLock() []dispatchPlan { //nolint:gocyclo // single-pass switch over all per-app statuses
	r.mu.Lock()
	defer r.mu.Unlock()

	// doDetach runs before the iteration tail (stepAllApps / tick / ...).
	// Without this guard, pre-RUNNING apps would spawn pull/start workers on
	// the dispatcher's Background context that survive detach. See the
	// `detaching` field doc on Runtime for the full rationale. tickUnderLock
	// has an equivalent check.
	if r.detaching.Load() {
		return nil
	}

	// During NS shutdown the graceful-shutdown group chain owns all
	// transitions. Apps in group[k>0] remain in their pre-stop status until
	// cmdStopNextGroup transitions them — stepAllApps must NOT advance them
	// forward (e.g. READY_TO_PULL → PULLING → ...) in the interim, otherwise
	// we'd spawn pull/start workers that fight the shutdown chain and leave
	// containers running past stop completion.
	if r.status == NsStatusStopping {
		return nil
	}

	now := r.nowFunc()
	var plans []dispatchPlan

	// T32: GC apps that were removed from the desired set by cmdRegenerate and
	// have reached STOPPED. STOPPING_FAILED apps with markedForRemoval=true are
	// NOT auto-deleted — they require operator attention. Delete-before-walk so
	// the subsequent switch doesn't re-process a just-GC'd entry.
	for name, app := range r.apps {
		if app.markedForRemoval && app.Status == AppStatusStopped {
			delete(r.apps, name)
		}
	}

	for _, app := range r.apps {
		// Detached apps are user-intent STOPPED — never advanced by the
		// state machine. Re-attach happens via StartApp (T27–T30).
		if r.manualStoppedApps[app.Name] {
			continue
		}
		switch app.Status {
		case AppStatusReadyToPull:
			appDef := app.Def
			if appDef.Image == "" {
				// T3 no-op branch: nothing to pull, advance to READY_TO_START.
				// No digest refresh possible without an image.
				r.setAppStatus(app, AppStatusReadyToStart)
				continue
			}
			pullAlways := shouldPullImage(appDef.Kind, appDef.Image)
			// T2/T3 unified dispatch: transition PULLING under Lock and emit a
			// pull plan. The worker's short-circuit path (runPullTask with
			// pullAlways=false) handles the T3 "image local, skip pull, refresh
			// digest" case off-Lock — ImageExists + GetImageDigest run inside
			// the worker goroutine, and handlePullResult's T5 applies the digest
			// and transitions READY_TO_START. Moving Docker I/O into the worker
			// keeps the Lock brief so SSE / HTTP / Apps() / ToNamespaceDto
			// readers don't stall during namespace start.
			r.setAppStatus(app, AppStatusPulling)
			progressFn := r.makePullProgressFn(app.Name)
			plans = append(plans, r.makePullPlan(app.Name, appDef.Image, pullAlways, progressFn))
		case AppStatusReadyToStart:
			// T7 vs T8: deps satisfied → STARTING (dispatch first init or
			// start), else → DEPS_WAITING.
			if !r.appsDepsSatisfied(app) {
				r.setAppStatus(app, AppStatusDepsWaiting)
				continue
			}
			plans = r.beginStartingUnderLock(app, plans)
		case AppStatusDepsWaiting:
			// T9: deps satisfied → STARTING. Same dispatch criterion as T7.
			if !r.appsDepsSatisfied(app) {
				continue
			}
			plans = r.beginStartingUnderLock(app, plans)
		case AppStatusPullFailed:
			// T24: backoff window elapsed → READY_TO_PULL. Counter is left
			// alone — a subsequent failure bumps it via recordRetryAttempt;
			// a success clears it via resetRetry (handlePullResult on T5).
			if r.retryDueFor(app.Name, now) {
				app.StatusText = ""
				r.setAppStatus(app, AppStatusReadyToPull)
			}
		case AppStatusStartFailed:
			// T25: backoff window elapsed → READY_TO_START. Same accounting
			// rules as T24.
			if r.retryDueFor(app.Name, now) {
				app.StatusText = ""
				r.setAppStatus(app, AppStatusReadyToStart)
			}
		default:
			// Other states handled in later phases (T17–T33).
		}
	}
	return plans
}

// beginStartingUnderLock transitions an app from READY_TO_START / DEPS_WAITING
// into STARTING and appends the first init-container plan (T7/T9) or the
// start plan (T7/T9 no-init shortcut). Caller must hold r.mu.Lock.
func (r *Runtime) beginStartingUnderLock(app *AppRuntime, plans []dispatchPlan) []dispatchPlan {
	app.initStepIdx = 0
	r.setAppStatus(app, AppStatusStarting)
	appDef := app.Def
	if len(appDef.InitContainers) == 0 {
		plans = append(plans, r.makeStartPlan(app.Name, appDef, r.volumesBase))
		return plans
	}
	initC := appDef.InitContainers[0]
	initDef := buildInitContainerDef(app.Name, initC)
	plans = append(plans, r.makeInitContainerPlan(app.Name, initC.Image, 0, initDef, r.volumesBase))
	return plans
}

// buildInitContainerDef materializes the appdef.ApplicationDef passed to the
// init-container worker. Shape matches init-container hashing / labeling.
func buildInitContainerDef(appName string, initC appdef.InitContainerDef) appdef.ApplicationDef {
	return appdef.ApplicationDef{
		Name: appName + "-init", Image: initC.Image,
		Cmd: initC.Cmd, Volumes: initC.Volumes, Environments: initC.Environments,
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "100m"}},
		IsInit:    true, // no restart policy for init containers
	}
}

// appsDepsSatisfied reports whether every dependency listed by app is in a
// state that lets app proceed past DEPS_WAITING. A dep is satisfied if it is
// (a) absent from r.apps (different mode/generation), (b) RUNNING, or
// (c) detached (manualStoppedApps). Caller must hold r.mu (read or write).
func (r *Runtime) appsDepsSatisfied(app *AppRuntime) bool {
	for dep := range app.Def.DependsOn {
		depApp, ok := r.apps[dep]
		if !ok {
			// Dep is not part of the current generation — treated as
			// satisfied so dependents don't stall (mirrors the historical
			// behavior in waitForDeps for cross-mode dep sets like
			// keycloak-in-BASIC-auth-mode).
			continue
		}
		if depApp.Status == AppStatusRunning {
			continue
		}
		if r.manualStoppedApps[dep] {
			continue
		}
		return false
	}
	return true
}

// makePullProgressFn returns a docker.PullProgressFn that updates app.StatusText
// under Lock. The closure captures app.Name (a string) rather than the
// *AppRuntime pointer so the progress callback does a fresh map lookup each
// time and doesn't touch a possibly-deleted app entry (e.g., post-regenerate).
func (r *Runtime) makePullProgressFn(appName string) docker.PullProgressFn {
	var lastReport time.Time
	return func(_, totalMB float64, pct int) {
		now := r.nowFunc()
		if now.Sub(lastReport) < time.Second {
			return
		}
		lastReport = now
		r.mu.Lock()
		if app, ok := r.apps[appName]; ok && app.Status == AppStatusPulling {
			app.StatusText = fmt.Sprintf("Pulling: %.0fmb %d%%", totalMB, pct)
		}
		r.mu.Unlock()
	}
}

// evaluateContinuations applies any continuations whose predicate has fired.
// Surviving commands are applied INLINE via applyCommand — never enqueued to
// cmdQueue. Snapshot-len-at-entry semantics guarantee that continuations
// appended during this pass (e.g. cmdStopNextGroup registering the next
// group's continuation) defer to the next iteration — bounded recursion, one
// group per tick.
//
// Predicate contract: pure reads over r; MUST NOT mutate.
//
// Lock discipline: predicates are called WITHOUT holding r.mu. applyCommand
// acquires r.mu.Lock internally when mutating state (see handleStopNextGroup).
// Predicates run via runtimeLoop (the sole state writer), so read-only
// predicate access to r.apps is race-free.
func (r *Runtime) evaluateContinuations() {
	if len(r.pendingContinuations) == 0 {
		return
	}
	// Snapshot length at entry so continuations appended during this pass
	// defer to the next iteration (bounded recursion; one group per tick).
	//
	// survivors aliases r.pendingContinuations' backing array — we filter in
	// place. The loop index i advances strictly from 0..n-1, so the rewrite
	// is safe: writes at survivors[0..k] can never overtake reads at [i>=k].
	// Any applyCommand below can append past n via handleStopNextGroup; those
	// entries are carried forward by the len(..) > n branch below.
	n := len(r.pendingContinuations)
	survivors := r.pendingContinuations[:0]
	for i := range n {
		p := r.pendingContinuations[i]
		// Predicate reads r.apps. Safe because runtimeLoop is the sole writer of
		// r.apps at this point (no dispatched worker holds r.mu.Lock here); the
		// RLock fences concurrent external readers (HTTP, SSE).
		r.mu.RLock()
		fired := p.predicate(r)
		r.mu.RUnlock()
		if fired {
			slog.Debug("continuation fired", "tag", p.tag, "cmd", p.cmd.cmdTag())
			r.applyCommand(p.cmd)
		} else {
			survivors = append(survivors, p)
		}
	}
	if len(r.pendingContinuations) > n {
		survivors = append(survivors, r.pendingContinuations[n:]...)
	}
	r.pendingContinuations = survivors
}

// applyCommand routes a runtimeCmd inline. cmdStopNextGroup is the sole
// internal continuation command; all others are externally enqueued.
// Unknown types are logged — they should never reach here if cmdQueue type
// discipline holds.
func (r *Runtime) applyCommand(cmd runtimeCmd) {
	switch c := cmd.(type) {
	case cmdStart:
		r.doStart(c.apps)
	case cmdRegenerate:
		if c.cfg != nil || (c.bundleDef != nil && !c.bundleDef.IsEmpty()) {
			r.mu.Lock()
			if c.cfg != nil {
				r.config = c.cfg
			}
			if c.bundleDef != nil && !c.bundleDef.IsEmpty() {
				r.cachedBundle = c.bundleDef
			}
			r.mu.Unlock()
		}
		r.doRegenerate(c.apps)
	case cmdStop:
		r.doStop()
	case cmdDetach:
		r.doDetach()
	case cmdStopNextGroup:
		r.handleStopNextGroup(c)
	default:
		// cmdStopApp / cmdStartApp / cmdRestartApp / cmdRetryPullFailed operate
		// inline under Lock; they are not currently enqueued. TODO: migrate
		// once daemon accepts eventual semantics.
		slog.Warn("applyCommand: unknown command type", "tag", cmd.cmdTag())
	}
}

// updateNsStatus re-derives the namespace-level status from per-app statuses.
// Holds r.mu.Lock for the whole operation: checkStatus reads r.apps and may
// call setStatus, which mutates r.status and buffers an event.
func (r *Runtime) updateNsStatus() {
	r.mu.Lock()
	r.checkStatus()
	r.mu.Unlock()
}

// flushEvents drains r.eventBuffer into r.eventCh in append order. Runs once
// per iteration at the end — events are emitted in the order they were
// buffered; no re-ordering, no de-dup, no mid-iteration flush.
//
// eventCh is buffered (cap 256) but flushEvents uses unconditional blocking
// sends to guarantee delivery — dropping a status event would surface as a UI
// regression. In practice an iteration buffers a small handful of events, so
// the loop never blocks for long.
func (r *Runtime) flushEvents() {
	r.mu.Lock()
	if len(r.eventBuffer) == 0 {
		r.mu.Unlock()
		return
	}
	events := r.eventBuffer
	r.eventBuffer = nil
	// Lock released intentionally before the channel sends below — the
	// sends MUST NOT hold r.mu or a slow subscriber would pin the state
	// machine's exclusion lock.
	r.mu.Unlock()
	for _, evt := range events {
		r.eventCh <- evt
	}
}

// applyWorkerResult dispatches a worker Result to its op-specific handler.
//
// Staleness check: if a newer attempt has superseded this taskID, drop the
// Result silently. Each op-specific handler MAY add an app-level re-check
// (e.g. OpPull must not fire T5/T6 if the app has transitioned to STOPPED).
//
// Detach drop: a Result canceled with reason=Detach AND that errored is
// dropped silently — preserves pre-detach state.
//
// After processing, ForgetTask frees the dispatcher slot.
func (r *Runtime) applyWorkerResult(res workers.Result) {
	if r.dispatcher.Current(res.TaskID) != res.AttemptID {
		// stale attempt — newer dispatch owns the slot; its ForgetTask will clean up.
		return
	}
	if res.Err != nil &&
		r.dispatcher.CancelReason(res.TaskID, res.AttemptID) == workers.CancelDetach {
		r.dispatcher.ForgetTask(res.TaskID, res.AttemptID)
		return
	}
	defer r.dispatcher.ForgetTask(res.TaskID, res.AttemptID)

	switch res.TaskID.Op {
	case workers.OpStats:
		r.handleStatsResult(res)
	case workers.OpPull:
		r.handlePullResult(res)
	case workers.OpInit:
		r.handleInitResult(res)
	case workers.OpStart:
		r.handleStartResult(res)
	case workers.OpProbe:
		r.handleProbeResult(res)
	case workers.OpStop:
		r.handleStopResult(res)
	case workers.OpRemoveNetwork:
		r.handleRemoveNetworkResult(res)
	case workers.OpReconcileDiff:
		r.handleReconcileDiffResult(res)
	case workers.OpLivenessProbe:
		r.handleLivenessProbeResult(res)
	case workers.OpPostStartActions:
		// Best-effort side-effect worker — no state transitions. The worker
		// logs exec errors and non-zero exits itself; the dispatcher slot is
		// released by the ForgetTask deferred above. No-op handler keeps the
		// switch exhaustive so applyWorkerResult doesn't fall into default.
	default:
		// Future phases handle other ops; ignore for now.
	}
}

// handleRemoveNetworkResult is the terminal step of the cmdStop continuation
// chain. A successful or failed RemoveNetwork both advance the runtime to
// NsStatusStopped — network removal is best-effort, matching the legacy
// doStop's `_ = r.docker.RemoveNetwork(netCtx)` semantics.
//
// Post-handling:
//   - Wipe apps map and all restart / liveness bookkeeping (matches legacy
//     doStop final-phase under Lock).
//   - Set NsStatusStopped.
//   - Record history entry.
//   - Signal shutdownComplete so runtimeLoop exits on its next iteration.
func (r *Runtime) handleRemoveNetworkResult(res workers.Result) {
	if res.Err != nil {
		// Best-effort: proceed with STOPPED regardless. Network may already
		// be gone, or Docker may surface a transient API error; neither
		// blocks shutdown.
		slog.Warn("RemoveNetwork failed (best-effort)", "err", res.Err)
	}
	r.mu.Lock()
	// Wipe apps + restart tracking (matches legacy doStop final-phase).
	r.apps = make(map[string]*AppRuntime)
	r.restartCounts = make(map[string]int)
	r.restartEvents = nil
	r.livenessFailures = make(map[string]int)
	r.setStatus(NsStatusStopped)
	r.mu.Unlock()

	if r.history != nil {
		r.history.Record("stop", "", "success", 0, nil, 0)
	}

	r.signalShutdown()
}

// handleStopNextGroup transitions groups[idx] into STOPPING (dispatching
// per-app stop workers) and registers the next continuation. When idx is
// past the last group, it dispatches the RemoveNetworkTask — the terminal
// step in the graceful-shutdown continuation chain.
func (r *Runtime) handleStopNextGroup(cmd cmdStopNextGroup) {
	// idx >= len(groups) → all groups drained; dispatch RemoveNetwork.
	if cmd.idx >= len(cmd.groups) {
		plan := r.makeRemoveNetworkPlan()
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
		return
	}

	r.mu.Lock()
	stopPlans := r.beginGroupStopUnderLock(cmd.groups[cmd.idx])
	// Capture idx by value so subsequent chain calls use the right group.
	currentIdx := cmd.idx
	nextIdx := cmd.idx + 1
	groups := cmd.groups
	r.pendingContinuations = append(r.pendingContinuations, continuation{
		predicate: func(rt *Runtime) bool { return allAppsTerminalInGroup(rt, groups, currentIdx) },
		cmd:       cmdStopNextGroup{idx: nextIdx, groups: groups},
		tag:       fmt.Sprintf("stop-group-%d", currentIdx),
	})
	r.mu.Unlock()

	for _, plan := range stopPlans {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
}

// allAppsTerminalInGroup returns true when every app in groups[idx] is in a
// terminal stop state (STOPPED or STOPPING_FAILED). Apps that have been
// removed from rt.apps count as terminal (already gone). Caller may hold
// r.mu read or write lock — the function only reads rt.apps.
func allAppsTerminalInGroup(rt *Runtime, groups [][]*AppRuntime, idx int) bool {
	if idx >= len(groups) {
		return true
	}
	for _, app := range groups[idx] {
		cur, ok := rt.apps[app.Name]
		if !ok {
			continue
		}
		if cur.Status != AppStatusStopped && cur.Status != AppStatusStoppingFailed {
			return false
		}
	}
	return true
}

// handleStopResult applies T21 (stopContainer Result OK → desiredNext if set,
// else STOPPED; clear ContainerID + initialSweep) and T22 (stopContainer
// Result error → STOPPING_FAILED; clear desiredNext; WARN).
//
// App-level guard: only STOPPING is a valid source state. Any other status
// means the state machine has moved on (rare — STOPPING is a terminal
// dispatch) and the Result is a no-op.
func (r *Runtime) handleStopResult(res workers.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	app, ok := r.apps[res.TaskID.App]
	if !ok || app.Status != AppStatusStopping {
		return
	}
	if res.Err != nil {
		// T22: STOPPING → STOPPING_FAILED. Drop desiredNext (restart intent
		// discarded; stuck state requires manual intervention via T30).
		priorDesiredNext := app.desiredNext
		app.desiredNext = ""
		app.initialSweep = false
		app.StatusText = res.Err.Error()
		slog.Warn("stop failed",
			"app", app.Name, "priorDesiredNext", string(priorDesiredNext), "err", res.Err)
		r.setAppStatus(app, AppStatusStoppingFailed)
		return
	}
	// T21: stopContainer succeeded. Clear ContainerID + initialSweep. If
	// desiredNext is set (restart-with-container-cleanup path: T17a, T30,
	// RestartApp), apply it; otherwise route to STOPPED.
	app.ContainerID = ""
	app.initialSweep = false
	app.StatusText = ""
	// If the user called StopApp while we were stopping (initial sweep,
	// T17a liveness restart, T30 retry, cmdRestartApp, etc.), manualStoppedApps
	// records the detach intent. That intent must override any queued
	// desiredNext — otherwise the app silently routes back up and the user's
	// stop is lost. Read under the lock we already hold.
	if r.manualStoppedApps[app.Name] {
		app.desiredNext = ""
		r.setAppStatus(app, AppStatusStopped)
		return
	}
	next := app.desiredNext
	app.desiredNext = ""
	if next != "" {
		r.setAppStatus(app, next)
	} else {
		r.setAppStatus(app, AppStatusStopped)
	}
}

// handleInitResult applies T10 (error → START_FAILED), T11 (success +
// more init pending → next init dispatch), T12 (success + last init →
// startContainer dispatch).
//
// App-level staleness guard: only STARTING is a valid source state; any other
// status means the state machine has moved on (e.g. StopApp raced — T19c).
// Drop the Result silently so a canceled init doesn't clobber a post-cancel
// state.
func (r *Runtime) handleInitResult(res workers.Result) {
	r.mu.Lock()
	app, ok := r.apps[res.TaskID.App]
	if !ok || app.Status != AppStatusStarting {
		r.mu.Unlock()
		return
	}
	if res.Err != nil {
		// T10: STARTING → START_FAILED. Reset chain index so a T25 retry
		// restarts init from the first container.
		app.initStepIdx = 0
		r.recordRetryAttempt(app.Name)
		app.StatusText = res.Err.Error()
		r.setAppStatus(app, AppStatusStartFailed)
		r.mu.Unlock()
		return
	}
	// Success: decide T11 (more pending) vs T12 (last init → start).
	payload, ok := res.Payload.(workers.InitPayload)
	if !ok {
		// Defensive: a successful init Result must carry InitPayload. If the
		// stamping ever breaks, completedIdx defaults to 0 and the chain
		// silently restarts from index 1. Surface the bug loudly and treat as
		// the T10 init-failure path so the app reaches a stable state.
		slog.Error("handleInitResult: missing InitPayload on successful init result", "app", res.TaskID.App)
		app.initStepIdx = 0
		r.recordRetryAttempt(app.Name)
		app.StatusText = "internal error: missing InitPayload"
		r.setAppStatus(app, AppStatusStartFailed)
		r.mu.Unlock()
		return
	}
	completedIdx := payload.Index
	appDef := app.Def
	nextIdx := completedIdx + 1
	var plan dispatchPlan
	if nextIdx < len(appDef.InitContainers) {
		// T11: dispatch next init container; status stays STARTING.
		app.initStepIdx = nextIdx
		initC := appDef.InitContainers[nextIdx]
		initDef := buildInitContainerDef(app.Name, initC)
		plan = r.makeInitContainerPlan(app.Name, initC.Image, nextIdx, initDef, r.volumesBase)
	} else {
		// T12: last init succeeded — dispatch startContainer; status stays STARTING.
		app.initStepIdx = 0
		plan = r.makeStartPlan(app.Name, appDef, r.volumesBase)
	}
	r.mu.Unlock()
	r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
}

// commitRunningUnderLock commits the RUNNING transition for app under the
// caller's held Lock and returns the inputs needed for an optional post-start
// actions dispatch after Unlock. Caller must hold r.mu.Lock and must Unlock
// before calling Dispatch. Returns (nil, "", false) when no dispatch is needed.
//
// Shared by T15 (handleStartResult) and T16 (handleProbeResult) which both
// commit RUNNING and optionally dispatch post-start init actions.
func (r *Runtime) commitRunningUnderLock(app *AppRuntime) (initActions []appdef.AppInitAction, containerID string, dispatch bool) {
	app.StatusText = ""
	app.initStepIdx = 0
	r.setAppStatus(app, AppStatusRunning)
	r.resetRetry(app.Name)
	if len(app.Def.InitActions) > 0 && app.ContainerID != "" {
		return app.Def.InitActions, app.ContainerID, true
	}
	return nil, "", false
}

// handleStartResult applies T13 (error → START_FAILED), T14 (success + has
// StartupConditions → dispatch probe), T15 (success + no probe → RUNNING).
// App-level staleness guard: only STARTING is a valid source state.
func (r *Runtime) handleStartResult(res workers.Result) {
	r.mu.Lock()
	app, ok := r.apps[res.TaskID.App]
	if !ok || app.Status != AppStatusStarting {
		r.mu.Unlock()
		return
	}
	if res.Err != nil {
		// T13: STARTING → START_FAILED.
		app.initStepIdx = 0
		r.recordRetryAttempt(app.Name)
		app.StatusText = res.Err.Error()
		r.setAppStatus(app, AppStatusStartFailed)
		r.mu.Unlock()
		return
	}
	payload, _ := res.Payload.(workers.StartPayload)
	app.ContainerID = payload.ContainerID
	appDef := app.Def
	containerID := payload.ContainerID
	if len(appDef.StartupConditions) > 0 {
		// T14: dispatch startup probe; status stays STARTING.
		plan := r.makeStartupProbePlan(app.Name, containerID, appDef.StartupConditions)
		r.mu.Unlock()
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
		return
	}
	// T15: no probe. Commit RUNNING and capture post-start dispatch inputs.
	initActions, cid, doDispatch := r.commitRunningUnderLock(app)
	r.mu.Unlock()
	if doDispatch {
		plan := r.makePostStartActionsPlan(res.TaskID.App, cid, initActions)
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
}

// handleProbeResult applies T16 (probe OK → run init actions inline → RUNNING)
// and T16b (probe error → START_FAILED).
//
// App-level staleness guard: only STARTING is a valid source state.
func (r *Runtime) handleProbeResult(res workers.Result) {
	r.mu.Lock()
	app, ok := r.apps[res.TaskID.App]
	if !ok || app.Status != AppStatusStarting {
		r.mu.Unlock()
		return
	}
	if res.Err != nil {
		// T16b: STARTING → START_FAILED.
		app.initStepIdx = 0
		r.recordRetryAttempt(app.Name)
		app.StatusText = res.Err.Error()
		r.setAppStatus(app, AppStatusStartFailed)
		r.mu.Unlock()
		return
	}
	// T16: commit RUNNING and capture post-start dispatch inputs.
	initActions, containerID, doDispatch := r.commitRunningUnderLock(app)
	r.mu.Unlock()
	if doDispatch {
		plan := r.makePostStartActionsPlan(res.TaskID.App, containerID, initActions)
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
}

// handlePullResult applies T5 (success → READY_TO_START) and T6 (error →
// PULL_FAILED) to the app whose pull finished. T4 (adopt-after-pull) is not
// implemented; see stepAllApps doc comment.
//
// App-level staleness guard: if the app has transitioned out of PULLING since
// this Result was scheduled (e.g. StopApp raced the pull — T19b), drop the
// Result. Terminal statuses include STOPPED, STOPPING, STOPPING_FAILED,
// FAILED — a pull success/failure must not revive a stopped app or clobber a
// post-cancel state.
func (r *Runtime) handlePullResult(res workers.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	app, ok := r.apps[res.TaskID.App]
	if !ok {
		return
	}
	// Only PULLING is a valid source for T5/T6. Any other status means the
	// state machine has moved on; drop the Result silently.
	if app.Status != AppStatusPulling {
		return
	}
	if res.Err != nil {
		// T6: PULLING → PULL_FAILED. T24 auto-retry runs via backoff.
		r.recordRetryAttempt(app.Name)
		app.StatusText = res.Err.Error()
		r.setAppStatus(app, AppStatusPullFailed)
		return
	}
	// T5: PULLING → READY_TO_START. Update ImageDigest from payload so the
	// deployment hash reflects what was actually pulled.
	if payload, pok := res.Payload.(workers.PullPayload); pok && payload.Digest != "" {
		appDef := app.Def
		appDef.ImageDigest = payload.Digest
		app.Def = appDef
	}
	app.StatusText = ""
	r.setAppStatus(app, AppStatusReadyToStart)
}

// handleStatsResult applies a successful StatsTask Result to the matching app.
// Errors are silently ignored to match legacy updateStats behavior (a missing
// container or transient docker-stats failure should not surface as a state
// transition).
func (r *Runtime) handleStatsResult(res workers.Result) {
	if res.Err != nil {
		return
	}
	payload, ok := res.Payload.(workers.StatsPayload)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if app, exists := r.apps[res.TaskID.App]; exists {
		app.CPU = payload.CPU
		app.Memory = payload.Memory
	}
}

// dispatchPlan is an immutable description of a worker task to dispatch.
// Produced under r.mu.Lock by tickUnderLock; consumed without the Lock by
// tickDispatch.
type dispatchPlan struct {
	taskID workers.TaskID
	fn     workers.TaskFunc
}

// tick is the per-second housekeeping driver: stats dispatch, T23 STOPPING
// budget enforcement, reconciler-diff scheduling, and per-app liveness probes.
func (r *Runtime) tick(ctx context.Context) {
	plans := r.tickUnderLock()
	r.tickDispatch(ctx, plans)
}

// tickUnderLock mutates state and accumulates worker dispatches to run after
// the Lock is released. The Lock fences concurrent RLock readers (Apps() /
// ToNamespaceDto()) — runtimeLoop is the sole state writer, but the Lock is
// what makes RLock snapshots coherent against the map writes.
func (r *Runtime) tickUnderLock() []dispatchPlan {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Mirror the detaching guard in stepAllAppsUnderLock: without this,
	// reconciler-diff / liveness / stats workers could be dispatched on the
	// dispatcher's Background context after doDetach has called CancelAll —
	// they'd survive detach.
	if r.detaching.Load() {
		return nil
	}

	now := r.nowFunc()
	var plans []dispatchPlan

	if now.Sub(r.lastStatsDispatch) >= r.statsInterval {
		for name, app := range r.apps {
			if app.Status != AppStatusRunning || app.ContainerID == "" {
				continue
			}
			plans = append(plans, r.makeStatsPlan(name, app.ContainerID))
		}
		r.lastStatsDispatch = now
	}

	// T23: scan STOPPING apps for budget exhaustion. The budget depends on
	// context:
	//   - initialSweep=true (runtime-initiated recreate): longStopTimeout
	//     (60s default — Java SIGTERM commonly takes 30–45s).
	//   - Normal stop (operator-initiated): resolveStopTimeout(app) + groupTimeout.
	//     resolveStopTimeout returns the per-app StopTimeout, or
	//     defaultStopTimeout (daemon.yml), or 0 — in which case Docker's own
	//     10s SIGTERM→SIGKILL window (dockerDefaultStop) is substituted so T23
	//     does not fire before Docker has a chance to kill the container.
	//
	// On timeout: cancel the in-flight stop worker, clear desiredNext, and
	// transition to STOPPING_FAILED. The canceled stopContainer's eventual
	// Result is dropped silently because handleStopResult's source-state
	// guard (Status==STOPPING) no longer matches.
	longBudget := r.longStopTimeout
	for _, app := range r.apps {
		if app.Status != AppStatusStopping {
			continue
		}
		var budget time.Duration
		if app.initialSweep {
			budget = longBudget
		} else {
			// Per-app budget: effective Docker stop window + groupTimeout buffer.
			// resolveStopTimeout returns 0 when nothing is configured; Docker
			// applies its own 10s SIGTERM→SIGKILL default in that case, so T23
			// must account for it to avoid false STOPPING_FAILED.
			const dockerDefaultStop = 10 * time.Second
			appTimeout := time.Duration(r.resolveStopTimeout(app.Def.StopTimeout)) * time.Second
			if appTimeout == 0 {
				appTimeout = dockerDefaultStop
			}
			budget = appTimeout + r.groupTimeout
		}
		if now.Sub(app.stoppingStartedAt) <= budget {
			continue
		}
		priorDesiredNext := app.desiredNext
		app.desiredNext = ""
		app.initialSweep = false
		app.StatusText = "stop timeout"
		slog.Warn("stop timeout exceeded",
			"app", app.Name, "budget", budget, "priorDesiredNext", string(priorDesiredNext))
		r.setAppStatus(app, AppStatusStoppingFailed)
		// Cancel the in-flight stop worker (best-effort; reason=ExternalStop
		// doesn't drop the Result, the source-state guard does).
		r.dispatcher.Cancel(workers.TaskID{App: app.Name, Op: workers.OpStop}, workers.CancelExternalStop)
	}

	// Reconciler-diff dispatch. Gated to NS status RUNNING/STALLED and to the
	// reconcilerEnabled flag (default true; daemon.yml reconciler.enabled:false
	// flips it via SetReconcilerConfig). Only one ReconcileDiffTask is in
	// flight per runtime at any time — the dispatcher deduplicates via TaskID
	// {App:"", Op:OpReconcileDiff} supersession.
	if r.reconcilerEnabled && (r.status == NsStatusRunning || r.status == NsStatusStalled) {
		if now.Sub(r.lastReconcileDispatch) >= r.reconcilerInterval {
			snapshot := make([]reconcileSnapshotEntry, 0, len(r.apps))
			for name, app := range r.apps {
				if app.Status != AppStatusRunning {
					continue
				}
				snapshot = append(snapshot, reconcileSnapshotEntry{Name: name, ContainerID: app.ContainerID})
			}
			plans = append(plans, r.makeReconcileDiffPlan(snapshot))
			r.lastReconcileDispatch = now
		}
	}

	// Per-app liveness probe scheduling. Each RUNNING app with a LivenessProbe
	// definition gets a LivenessProbeTask dispatched when its next-scheduled
	// time has elapsed. livenessNextAt tracks the next scheduled time per app;
	// on RUNNING transition setAppStatus seeds it with an InitialDelaySeconds
	// offset. The next schedule is set right before dispatch so a still-in-
	// flight probe doesn't get re-dispatched.
	//
	// Gated by livenessEnabled (default true; daemon.yml
	// reconciler.livenessEnabled:false flips it via SetReconcilerConfig).
	//
	// If a probe runs longer than Period, the next tick re-dispatches and
	// dispatcher supersession cancels the in-flight attempt (kubelet-style).
	if r.livenessEnabled && (r.status == NsStatusRunning || r.status == NsStatusStalled) {
		for name, app := range r.apps {
			if app.Status != AppStatusRunning || app.ContainerID == "" {
				continue
			}
			if app.Def.LivenessProbe == nil {
				continue
			}
			nextAt, ok := r.livenessNextAt[name]
			if !ok {
				// Should have been seeded by setAppStatus on RUNNING transition.
				// Defensive: seed here so we don't spam an unseeded slot.
				nextAt = now.Add(initialDelayForProbe(app.Def.LivenessProbe))
				r.livenessNextAt[name] = nextAt
				continue
			}
			if now.Before(nextAt) {
				continue
			}
			plans = append(plans, r.makeLivenessProbePlan(name, app.ContainerID, app.Def.LivenessProbe))
			r.livenessNextAt[name] = now.Add(r.periodForProbe(app.Def.LivenessProbe))
		}
	}

	return plans
}

// initialDelayForProbe returns the initial delay before the first liveness
// probe for an app. Default 5s matches common container orchestrators.
func initialDelayForProbe(p *appdef.AppProbeDef) time.Duration {
	if p == nil || p.InitialDelaySeconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(p.InitialDelaySeconds) * time.Second
}

// periodForProbe returns the period between liveness probes for an app.
// Resolution order:
//  1. Per-app AppProbeDef.PeriodSeconds (authoritative when set).
//  2. Global daemon.yml reconciler.livenessPeriod fallback, if configured via
//     SetReconcilerConfig (pre-Phase-6 compatibility knob).
//  3. 10s default — matches common container orchestrators.
func (r *Runtime) periodForProbe(p *appdef.AppProbeDef) time.Duration {
	if p != nil && p.PeriodSeconds > 0 {
		return time.Duration(p.PeriodSeconds) * time.Second
	}
	if r.reconcilerCfg != nil && r.reconcilerCfg.LivenessPeriod > 0 {
		return r.reconcilerCfg.LivenessPeriod
	}
	return 10 * time.Second
}

// tickDispatch fires accumulated worker tasks without holding r.mu.
func (r *Runtime) tickDispatch(_ context.Context, plans []dispatchPlan) {
	for _, p := range plans {
		r.dispatcher.Dispatch(p.taskID, p.fn, r.resultCh, r.signalCh)
	}
}

// makeStatsPlan returns a dispatchPlan for fetching docker stats on the given
// container. The TaskFunc wraps the docker call in a pprof label so the
// stats workers are observable via runtime/pprof.GoroutineProfile (used by
// TestShutdownDoesNotLeakStatsGoroutine).
func (r *Runtime) makeStatsPlan(appName, containerID string) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpStats},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", "citeck-runtime-stats")
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.fetchContainerStats(ctx, containerID)
			})
			return result
		},
	}
}

// fetchContainerStats invokes docker stats and shapes the StatsPayload. Errors
// are returned to applyWorkerResult, which silently drops them (matches the
// legacy updateStats behavior — missing containers and transient API failures
// are not state transitions).
func (r *Runtime) fetchContainerStats(ctx context.Context, containerID string) workers.Result {
	statsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stats, err := r.docker.ContainerStats(statsCtx, containerID)
	if err != nil {
		return workers.Result{Err: err}
	}
	return workers.Result{Payload: workers.StatsPayload{
		CPU:    fmt.Sprintf("%.1f%%", stats.CPUPercent),
		Memory: formatMemory(stats.MemUsage, stats.MemLimit),
	}}
}

// handleReconcileDiffResult applies T18 (reconciler-detected crash / OOM →
// READY_TO_PULL + restart_event) for each app the worker reported as missing.
// Re-checks under Lock that the app is still RUNNING and still not in the
// running-set of the reported snapshot — the handler trusts the worker's
// Missing list on the understanding that a concurrent StartApp/StopApp path
// would have transitioned the app away from RUNNING, which this re-check
// catches.
//
// Runs entirely under r.mu.Lock except for the signalCh.Flush after release.
func (r *Runtime) handleReconcileDiffResult(res workers.Result) {
	if res.Err != nil {
		slog.Warn("reconcile-diff failed", "err", res.Err)
		return
	}
	payload, ok := res.Payload.(workers.ReconcileDiffPayload)
	if !ok || len(payload.Missing) == 0 {
		return
	}
	r.cleanupOldDiagnostics()

	r.mu.Lock()
	transitioned := false
	for _, m := range payload.Missing {
		app, exists := r.apps[m.Name]
		if !exists || app.Status != AppStatusRunning {
			// App's state changed between snapshot and result — skip.
			continue
		}
		var reason, detail string
		if m.OOMKilled {
			reason = "oom"
			detail = "container OOM killed"
			slog.Warn("Reconciler: container OOM killed, will restart", "app", m.Name)
			r.emitEvent(api.EventDto{
				Type: "app_oom", Timestamp: r.nowFunc().UnixMilli(),
				NamespaceID: r.nsID, AppName: m.Name, After: "OOMKilled",
			})
		} else {
			reason = "crash"
			detail = "container disappeared"
			slog.Warn("Reconciler: container missing, will restart", "app", m.Name)
		}
		r.incrementRestartCount(m.Name)
		r.emitRestartEvent(app, reason, detail, "")
		// T18: clear ContainerID and transition to READY_TO_PULL. Container
		// is already gone — no stop dispatch needed. State machine's next
		// stepAllApps iteration picks up READY_TO_PULL → T2/T3.
		app.ContainerID = ""
		r.setAppStatus(app, AppStatusReadyToPull)
		transitioned = true
	}
	if transitioned {
		// incrementRestartCount + emitRestartEvent mutate persistable state
		// (restartCounts, restartEvents); flip dirty so the loop tail persists
		// once per iteration. setAppStatus only buffers events and does NOT
		// flip dirty on its own — per-app Status itself is not persisted.
		r.dirty.Store(true)
	}
	r.mu.Unlock()

	if transitioned {
		// Wake loop so T2/T3 fires promptly rather than on the next 1s tick.
		r.signalCh.Flush()
	}
}

// handleLivenessProbeResult applies liveness-probe outcome to the
// livenessFailures counter. On success: reset counter (no transition). On
// failure: increment counter; if it reaches FailureThreshold, fire T17a
// (STOPPING + desiredNext=READY_TO_PULL + restart_event + stopContainer
// dispatch).
//
// App-level staleness guard: if the app has transitioned out of RUNNING
// (concurrent StopApp, etc.), drop the Result silently — no counter update.
func (r *Runtime) handleLivenessProbeResult(res workers.Result) {
	payload, ok := res.Payload.(workers.LivenessProbePayload)
	healthy := false
	if ok {
		healthy = payload.Healthy
	}
	// Non-nil Err is treated as unhealthy (preserves legacy behavior where a
	// probe invocation error counted as a failure).
	if res.Err != nil {
		healthy = false
	}

	appName := res.TaskID.App

	r.mu.Lock()
	app, exists := r.apps[appName]
	if !exists || app.Status != AppStatusRunning {
		r.mu.Unlock()
		return
	}

	if healthy {
		delete(r.livenessFailures, appName)
		r.mu.Unlock()
		return
	}

	// Probe failed — bump counter.
	r.livenessFailures[appName]++
	failures := r.livenessFailures[appName]
	threshold := app.Def.LivenessProbe.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}

	if failures < threshold {
		slog.Warn("Liveness probe failed (below threshold)",
			"app", appName, "failures", failures, "threshold", threshold)
		r.mu.Unlock()
		return
	}

	// Threshold reached — capture inputs for diagnostics and stop dispatch,
	// then release Lock for the slow diagnostic capture.
	containerID := app.ContainerID
	isCiteck := app.Def.Kind.IsCiteckApp()
	slog.Error("Liveness probe failed, restarting app",
		"app", appName, "failures", failures, "threshold", threshold)
	r.mu.Unlock()

	// Capture diagnostics outside lock (may run Docker commands). Use the
	// dispatcher's parent context via Background — matches the legacy behavior.
	reason := fmt.Sprintf("liveness probe failed %d/%d", failures, threshold)
	diag := r.captureDiagnostics(context.Background(), appName, containerID, isCiteck, reason)

	r.mu.Lock()
	// Re-verify app is still RUNNING after diagnostics.
	app, exists = r.apps[appName]
	if !exists || app.Status != AppStatusRunning {
		r.mu.Unlock()
		return
	}
	// T17a: RUNNING → STOPPING; desiredNext=READY_TO_PULL so T21 routes
	// through READY_TO_PULL → T2/T3 → start.
	app.desiredNext = AppStatusReadyToPull
	app.stoppingStartedAt = r.nowFunc()
	r.incrementRestartCount(appName)
	r.emitRestartEvent(app, "liveness", reason, diag)
	r.setAppStatus(app, AppStatusStopping)
	delete(r.livenessFailures, appName)
	// incrementRestartCount + emitRestartEvent mutate persistable state
	// (restartCounts, restartEvents); flip dirty so the loop tail persists
	// this transition. setAppStatus itself does NOT flip dirty (per-app Status
	// is not persisted), so without this the restart_event would be lost on a
	// crash before the next tick.
	r.dirty.Store(true)

	stopTimeout := r.resolveStopTimeout(app.Def.StopTimeout)
	containerName := r.docker.ContainerName(appName)
	plan := r.makeStopPlan(appName, containerName, stopTimeout)
	r.mu.Unlock()

	r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	r.signalCh.Flush()
}
