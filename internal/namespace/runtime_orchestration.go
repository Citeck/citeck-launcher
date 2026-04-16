package namespace

import (
	"context"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// staleSweepPlan is an accumulator emitted during doStart for each app whose
// existing container must be stopped+removed before the state machine can
// drive pull/start. Populated under r.mu.Lock; consumed without the Lock
// (dispatches stopContainer workers) after Unlock.
type staleSweepPlan struct {
	appName       string
	containerName string
	stopTimeout   int
}

func (r *Runtime) doStart(apps []appdef.ApplicationDef) { //nolint:gocyclo // orchestration with 3-phase lock pattern
	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.runCtx = ctx
	r.cancel = cancel
	r.lastApps = apps
	r.livenessFailures = make(map[string]int)
	r.setStatus(NsStatusStarting)
	r.mu.Unlock()

	// Create network
	if _, err := r.docker.CreateNetwork(ctx); err != nil {
		slog.Error("Failed to create network", "err", err)
	}

	// Check existing containers for deployment hash match
	existingContainers := r.buildExistingContainerMap(ctx)

	// No-lock phase: resolve image digests and compute hashes.
	// This avoids holding the mutex during Docker API calls.
	r.mu.RLock()
	editedLocked := make(map[string]bool, len(r.editedLockedApps))
	editedApps := make(map[string]appdef.ApplicationDef, len(r.editedApps))
	detached := make(map[string]bool, len(r.manualStoppedApps))
	maps.Copy(editedLocked, r.editedLockedApps)
	maps.Copy(editedApps, r.editedApps)
	maps.Copy(detached, r.manualStoppedApps)
	r.mu.RUnlock()

	// Refresh local digests for :snapshot images by pulling from registry
	// first. Without this, the hash-diff below would compute hash from a
	// stale local digest — adoption would keep running the old container
	// even though the developer pushed a new image under the same tag.
	r.refreshSnapshotDigests(ctx, apps)

	type appPlan struct {
		def           appdef.ApplicationDef
		hash          string
		containerName string
		reuse         bool   // true = keep running, false = recreate
		containerID   string // set when reusing
	}
	plans := make([]appPlan, 0, len(apps))

	for _, appDef := range apps {
		if editedLocked[appDef.Name] {
			if edited, ok := editedApps[appDef.Name]; ok {
				slog.Info("Applying locked edit override", "app", appDef.Name)
				appDef = edited
			}
		}
		// For :snapshot images, drop any cached digest so we pick up the
		// just-refreshed local value (see refreshSnapshotDigests above).
		if shouldPullImage(appDef.Kind, appDef.Image) {
			appDef.ImageDigest = ""
		}
		// Resolve image digest from local Docker cache (no lock needed)
		if appDef.ImageDigest == "" {
			if digest := r.docker.GetImageDigest(ctx, appDef.Image); digest != "" {
				appDef.ImageDigest = digest
			}
		}
		hash := appDef.GetHash()
		containerName := r.docker.ContainerName(appDef.Name)

		plan := appPlan{def: appDef, hash: hash, containerName: containerName}
		if !detached[appDef.Name] {
			if existing, ok := existingContainers[appDef.Name]; ok && existing.hash == hash && existing.running {
				plan.reuse = true
				plan.containerID = existing.containerID
			}
		}
		plans = append(plans, plan)
	}

	// If gateway is being recreated, proxy must also be recreated — nginx caches
	// upstream DNS at startup and won't follow gateway's new IP.
	gatewayRecreated := false
	for _, p := range plans {
		if p.def.Name == appdef.AppGateway && !p.reuse {
			gatewayRecreated = true
			break
		}
	}
	if gatewayRecreated {
		for i, p := range plans {
			if p.def.Name == appdef.AppProxy && p.reuse {
				slog.Info("Recreating proxy because gateway was recreated (nginx DNS cache)")
				plans[i].reuse = false
				plans[i].containerID = ""
				break
			}
		}
	}

	// Legacy cleanup for containers NOT in the desired set — they have no
	// AppRuntime entry so the state machine can't drive them. Stale containers
	// whose app IS still in the desired set are handled via the state machine
	// below (STOPPING + initialSweep=true + desiredNext=READY_TO_PULL).
	desiredNames := make(map[string]bool, len(plans))
	for _, p := range plans {
		desiredNames[p.def.Name] = true
	}
	var removeWg sync.WaitGroup
	for name := range existingContainers {
		if !desiredNames[name] {
			containerName := r.docker.ContainerName(name)
			removeWg.Add(1)
			go func(cn string) {
				defer removeWg.Done()
				_ = r.docker.StopAndRemoveContainer(ctx, cn, 0)
			}(containerName)
		}
	}
	removeWg.Wait()

	// Verify reused containers are actually running (fast Docker inspect).
	// Note: we intentionally do NOT run a synchronous liveness probe here —
	// under reload stress the probe can flake, causing unnecessary recreates.
	// Truly-hung containers (running but unresponsive) are caught by the
	// reconciler's periodic liveness probe within ~FailureThreshold × periodSeconds.
	for i, p := range plans {
		if !p.reuse {
			continue
		}
		inspCtx, inspCancel := context.WithTimeout(ctx, 5*time.Second)
		info, err := r.docker.InspectContainer(inspCtx, p.containerID)
		inspCancel()
		if err != nil || info.State == nil || info.State.Status != "running" {
			slog.Warn("Reused container not running, will recreate", "app", p.def.Name)
			plans[i].reuse = false
			plans[i].containerID = ""
			continue
		}
	}

	// Lock phase: atomically replace in-memory state and launch apps.
	// Uses the same `detached` snapshot as the no-lock phases above for
	// consistency — any concurrent Stop/StartApp during this doStart pass is
	// applied on the next regeneration cycle (the caller marks intent in
	// manualStoppedApps directly, next reload/regenerate will propagate).
	//
	// Stale containers (existing && !reuse && !detached) enter as STOPPING
	// with desiredNext=READY_TO_PULL and initialSweep=true. The state machine
	// drives the sweep via T21 → READY_TO_PULL → T2/T3 → ... → RUNNING.
	// tick() T23 uses longStopTimeout (60s) on initialSweep apps to
	// accommodate Java SIGTERM handlers.
	now := r.nowFunc()
	r.mu.Lock()
	var sweepPlans []staleSweepPlan
	newApps := make(map[string]*AppRuntime, len(plans))
	for _, p := range plans {
		switch {
		case detached[p.def.Name]:
			newApps[p.def.Name] = &AppRuntime{
				Name: p.def.Name, Status: AppStatusStopped, Def: p.def,
			}
		case p.reuse:
			slog.Info("Reusing existing container (hash match)", "app", p.def.Name)
			adopted := &AppRuntime{
				Name: p.def.Name, Status: AppStatusRunning, Def: p.def,
				ContainerID: p.containerID,
			}
			newApps[p.def.Name] = adopted
			// T33: if the most recent restart_event for this app indicates a
			// prior failure (liveness / crash / oom), emit a readopted_failing
			// observability event. Order is load-bearing: snapshot
			// lastRestartReason BEFORE emit (the emit appends readopted_failing,
			// which would otherwise mask the bad reason). Self-mutes via the
			// readopted_failing reason itself.
			priorReason := r.lastRestartReason(p.def.Name)
			switch priorReason {
			case "liveness", "crash", "oom":
				slog.Warn("re-adopting previously failing container",
					"app", p.def.Name, "priorReason", priorReason)
				r.emitRestartEvent(adopted, "readopted_failing", "", "")
				// emitRestartEvent mutates restartEvents (persistable). A
				// downstream setStatus(NsStatusRunning) usually flips dirty
				// too, but mark dirty explicitly so the loop tail persists
				// this event even if the flow shortcuts past the NS status
				// transition.
				r.dirty.Store(true)
			}
		default:
			// p.reuse == false && !detached.
			if existing, ok := existingContainers[p.def.Name]; ok {
				// Stale container present — schedule state-machine recreate.
				// Enter STOPPING with desiredNext=READY_TO_PULL and
				// initialSweep=true so tick() T23 uses longStopTimeout
				// (default 60s — Java SIGTERM tolerance). ContainerID is
				// carried through so handleStopResult can clear it on T21.
				slog.Info("Scheduling stale container sweep via state machine", "app", p.def.Name)
				stopTimeout := r.resolveStopTimeout(p.def.StopTimeout)
				newApps[p.def.Name] = &AppRuntime{
					Name:              p.def.Name,
					Status:            AppStatusStopping,
					StatusText:        "initial sweep",
					Def:               p.def,
					ContainerID:       existing.containerID,
					desiredNext:       AppStatusReadyToPull,
					initialSweep:      true,
					stoppingStartedAt: now,
				}
				sweepPlans = append(sweepPlans, staleSweepPlan{
					appName:       p.def.Name,
					containerName: p.containerName,
					stopTimeout:   stopTimeout,
				})
			} else {
				// Fresh app (no prior container): READY_TO_PULL direct.
				newApps[p.def.Name] = &AppRuntime{
					Name: p.def.Name, Status: AppStatusReadyToPull, Def: p.def,
				}
			}
		}
	}
	r.apps = newApps
	// READY_TO_PULL → PULLING is owned by the state machine (T2 in
	// stepAllApps); picked up on the next runtimeLoop iteration.
	r.mu.Unlock()

	// Dispatch stale-sweep stopContainer workers outside the lock. Their
	// Results flow through applyWorkerResult → handleStopResult, which
	// applies T21 (success → desiredNext=READY_TO_PULL) or T22 (err →
	// STOPPING_FAILED).
	for _, s := range sweepPlans {
		plan := r.makeStopPlan(s.appName, s.containerName, s.stopTimeout)
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}

	// Wake the runtime loop so stepAllApps fires T2 promptly rather than on
	// the next 1s tick.
	r.signalCh.Flush()

	// Reconcile-diff and liveness probes run as tick()-dispatched workers from
	// runtimeLoop (see tickUnderLock). SetReconcilerConfig wires the interval
	// knobs directly onto the Runtime; no goroutine to start here.

	// Record start operation
	if r.history != nil {
		r.history.Record("start", "", "initiated", 0, nil, len(apps))
	}
}

// doRegenerate applies a new set of app definitions like docker-compose up.
// Unchanged-hash apps are left untouched; changed-hash apps enter STOPPING
// with desiredNext=READY_TO_PULL and initialSweep=true (long-stop budget —
// Java webapps tolerate 30–45s SIGTERM); removed apps get
// markedForRemoval=true and drive to STOPPED where T32 GCs them.
//
// Unlike doStart, doRegenerate does NOT call buildExistingContainerMap — it
// trusts the current r.apps map (populated by the previous doStart).
//
// Lock discipline:
//  1. I/O outside the lock: clone snapshot maps under RLock, resolve image
//     digests (Docker calls), compute hashes.
//  2. Lock + per-app diff + state transitions + dispatch plan accumulation.
//  3. Unlock, then dispatch stopContainer workers.
func (r *Runtime) doRegenerate(apps []appdef.ApplicationDef) { //nolint:gocyclo // single-pass per-app diff over the new desired set
	// Reuse the existing runCtx — the reconciler continues running against the
	// same context and observes r.apps mutations atomically under the
	// runtimeLoop's single-writer rule. Workers dispatched via
	// r.dispatcher use their own per-task contexts (parent = Background), so
	// they're unaffected by runCtx at all.
	//
	// In-flight pulls/starts/probes for changed-hash apps are canceled
	// per-app below via r.dispatcher.CancelApp.
	r.mu.Lock()
	ctx := r.runCtx
	if ctx == nil {
		// Defensive: if regenerate lands before any doStart (shouldn't
		// happen in production — Regenerate is gated on a prior Start),
		// synthesize a background ctx so Docker calls below don't panic on
		// a nil ctx. The reconciler remains not-started; that's acceptable
		// for an API-misuse path.
		ctx = context.Background()
	}
	r.lastApps = apps
	// Clean slate for retry tracking — regeneration resets counters.
	r.retryState = nil
	r.mu.Unlock()

	// No-lock phase: clone snapshot maps + resolve image digests.
	r.mu.RLock()
	editedLocked := maps.Clone(r.editedLockedApps)
	editedApps := maps.Clone(r.editedApps)
	detached := maps.Clone(r.manualStoppedApps)
	r.mu.RUnlock()

	// Refresh local digests for :snapshot images before computing the hash
	// diff. Without this, reload would never detect a dev-pushed snapshot
	// that reuses the same tag — hash(stale local digest) would match the
	// running container's stored hash and doRegenerate would skip it.
	r.refreshSnapshotDigests(ctx, apps)

	type resolvedApp struct {
		def  appdef.ApplicationDef
		hash string
	}
	resolved := make([]resolvedApp, 0, len(apps))
	for _, appDef := range apps {
		if editedLocked[appDef.Name] {
			if edited, ok := editedApps[appDef.Name]; ok {
				slog.Info("Applying locked edit override", "app", appDef.Name)
				appDef = edited
			}
		}
		// For :snapshot images, drop any cached digest so we pick up the
		// just-refreshed local value (see refreshSnapshotDigests above).
		if shouldPullImage(appDef.Kind, appDef.Image) {
			appDef.ImageDigest = ""
		}
		if appDef.ImageDigest == "" {
			if digest := r.docker.GetImageDigest(ctx, appDef.Image); digest != "" {
				appDef.ImageDigest = digest
			}
		}
		resolved = append(resolved, resolvedApp{def: appDef, hash: appDef.GetHash()})
	}
	desiredNames := make(map[string]bool, len(resolved))
	for _, ra := range resolved {
		desiredNames[ra.def.Name] = true
	}

	// Lock phase: per-app diff + state transitions + dispatch plans.
	now := r.nowFunc()
	r.mu.Lock()

	// NS status: mark STARTING so updateNsStatus picks RUNNING again once the
	// state machine walks recreated apps to RUNNING. Unchanged-hash-only
	// regenerate will immediately re-derive RUNNING on the next
	// updateNsStatus — acceptable tiny blip.
	r.setStatus(NsStatusStarting)

	var stopPlans []dispatchPlan

	for _, ra := range resolved {
		existing, inApps := r.apps[ra.def.Name]
		if !inApps {
			// Fresh app: READY_TO_PULL (or STOPPED if detached). State
			// machine drives from there.
			if detached[ra.def.Name] {
				r.apps[ra.def.Name] = &AppRuntime{
					Name: ra.def.Name, Status: AppStatusStopped, Def: ra.def,
				}
			} else {
				r.apps[ra.def.Name] = &AppRuntime{
					Name: ra.def.Name, Status: AppStatusReadyToPull, Def: ra.def,
				}
			}
			continue
		}

		// Existing app: hash unchanged → no-op (refresh def so non-hash
		// fields like progress text don't clobber).
		if existing.Def.GetHash() == ra.hash {
			existing.Def = ra.def
			continue
		}

		// Hash changed: queue a recreate via STOPPING + desiredNext=READY_TO_PULL.
		// initialSweep=true selects the long-stop budget for Java SIGTERM tolerance.
		existing.Def = ra.def
		existing.StatusText = ""

		if existing.ContainerID != "" {
			// Dispatch stopContainer. Cancel any in-flight pull/init/start/
			// probe (spare OpStop so a prior stop can retire naturally — the
			// new Dispatch supersedes it via attemptID bump).
			r.dispatcher.CancelApp(existing.Name, workers.CancelExternalStop, workers.OpStop)
			existing.desiredNext = AppStatusReadyToPull
			existing.initialSweep = true
			existing.stoppingStartedAt = now
			r.setAppStatus(existing, AppStatusStopping)
			stopTimeout := r.resolveStopTimeout(existing.Def.StopTimeout)
			containerName := r.docker.ContainerName(existing.Name)
			stopPlans = append(stopPlans, r.makeStopPlan(existing.Name, containerName, stopTimeout))
		} else {
			// No container to stop. Cancel any in-flight worker and go straight
			// to READY_TO_PULL so the state machine re-pulls + starts with the
			// new def.
			r.dispatcher.CancelApp(existing.Name, workers.CancelExternalStop)
			existing.desiredNext = ""
			existing.initialSweep = false
			r.setAppStatus(existing, AppStatusReadyToPull)
		}
	}

	// Removed apps: in r.apps but NOT in the new desired set. Mark for
	// removal + drive to STOPPED (T32 GC runs in stepAllApps once terminal).
	for name, app := range r.apps {
		if desiredNames[name] {
			continue
		}
		app.markedForRemoval = true
		app.desiredNext = ""
		app.initialSweep = false
		if app.ContainerID != "" {
			app.stoppingStartedAt = now
			r.dispatcher.CancelApp(name, workers.CancelExternalStop, workers.OpStop)
			r.setAppStatus(app, AppStatusStopping)
			stopTimeout := r.resolveStopTimeout(app.Def.StopTimeout)
			containerName := r.docker.ContainerName(name)
			stopPlans = append(stopPlans, r.makeStopPlan(name, containerName, stopTimeout))
		} else {
			// No container → STOPPED directly. T32 GCs the entry on the next
			// stepAllApps iteration.
			r.dispatcher.CancelApp(name, workers.CancelExternalStop)
			r.setAppStatus(app, AppStatusStopped)
		}
	}
	r.mu.Unlock()

	// Dispatch stop workers outside the lock. Their Results flow through
	// applyWorkerResult → handleStopResult (T21/T22).
	for _, plan := range stopPlans {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}
	// Wake loop so stepAllApps fires transitions promptly.
	r.signalCh.Flush()

	if r.history != nil {
		r.history.Record("regenerate", "", "initiated", 0, nil, len(apps))
	}
}

// doStop initiates a graceful namespace shutdown via the state machine:
//
//  1. Mark NS as STOPPING, cancel runCtx (stops reconciler + appWg goroutines).
//  2. Partition live apps into graceful-shutdown groups.
//  3. Transition group[0] apps to STOPPING via beginGroupStopUnderLock;
//     dispatch their stopContainer workers.
//  4. Register a continuation: when all of group[0] is terminal, fire
//     cmdStopNextGroup{idx:1, groups} — which processes group 1, registers
//     the next continuation, and so on until idx >= len(groups).
//  5. The tail continuation dispatches RemoveNetworkTask.
//     handleRemoveNetworkResult wipes r.apps, sets NsStatusStopped, and calls
//     signalShutdown() — which closes shutdownComplete and lets runtimeLoop
//     exit on the next iteration.
//
// r.mu is released between every Docker call so Apps() / Status() remain
// responsive throughout shutdown. doStop returns quickly (non-blocking);
// runtimeLoop exits only when shutdownComplete is closed.
func (r *Runtime) doStop() {
	r.mu.Lock()
	if r.status == NsStatusStopping {
		// Shutdown chain already in flight from a prior Stop(). Idempotent:
		// don't re-run beginGroupStopUnderLock, don't register a second
		// continuation (which would duplicate the "stop initiated" history
		// entry and re-dispatch RemoveNetwork at the chain's tail).
		r.mu.Unlock()
		return
	}
	r.setStatus(NsStatusStopping)
	if r.cancel != nil {
		// Cancels runCtx — reconciler + any appWg goroutines observe
		// ctx.Done and exit. Their Wait() happens in shutdownAfter.
		r.cancel()
		r.cancel = nil
	}

	// Snapshot current apps into graceful-shutdown groups.
	toStop := make([]*AppRuntime, 0, len(r.apps))
	for _, app := range r.apps {
		toStop = append(toStop, app)
	}
	groups := GracefulShutdownGroups(toStop)

	var stopPlans []dispatchPlan
	if len(groups) > 0 {
		stopPlans = r.beginGroupStopUnderLock(groups[0])
	}

	// Register continuation: when group[0] is drained, transition to
	// group[1]. The chain continues until idx >= len(groups) at which
	// point handleStopNextGroup dispatches RemoveNetworkTask.
	groupsSnap := groups
	r.pendingContinuations = append(r.pendingContinuations, continuation{
		predicate: func(rt *Runtime) bool { return allAppsTerminalInGroup(rt, groupsSnap, 0) },
		cmd:       cmdStopNextGroup{idx: 1, groups: groupsSnap},
		tag:       "stop-group-0",
	})
	r.mu.Unlock()

	// Dispatch group[0] stop workers outside the lock. Their Results flow
	// through applyWorkerResult → handleStopResult, driving T21/T22. Once
	// all are terminal, evaluateContinuations fires cmdStopNextGroup{1,...}.
	for _, plan := range stopPlans {
		r.dispatcher.Dispatch(plan.taskID, plan.fn, r.resultCh, r.signalCh)
	}

	if r.history != nil {
		r.history.Record("stop", "", "initiated", 0, nil, len(toStop))
	}
}

// beginGroupStopUnderLock transitions each app in the group into STOPPING
// (T20 / T20b / T20c) and accumulates stopContainer plans to dispatch after
// the Lock is released. Must be called under r.mu.Lock. Returns plans for
// the caller to Dispatch outside the Lock.
//
// Per-app routing:
//   - RUNNING / FAILED / START_FAILED: T20 (STOPPING + stopContainer).
//   - STARTING with ContainerID != "": T20 (main container exists).
//   - STARTING with ContainerID == "": T20c (init-phase; cancel init worker,
//     stop the `{appName}-init` container).
//   - STOPPING_FAILED: fresh T20 (re-dispatch stopContainer; attemptID bump
//     supersedes any stale entry).
//   - PULLING / READY_TO_PULL / READY_TO_START / DEPS_WAITING / PULL_FAILED:
//     T20b (cancel in-flight pulls; STOPPED directly — no container exists).
//   - STOPPING: already in flight; skip (its own Result counts toward group).
//   - STOPPED: already terminal; skip.
func (r *Runtime) beginGroupStopUnderLock(group []*AppRuntime) []dispatchPlan {
	plans := make([]dispatchPlan, 0, len(group))
	now := r.nowFunc()
	for _, app := range group {
		switch app.Status {
		case AppStatusRunning, AppStatusFailed, AppStatusStartFailed:
			// T20: main container exists; dispatch stopContainer by name.
			app.desiredNext = ""
			app.stoppingStartedAt = now
			app.initialSweep = false
			r.setAppStatus(app, AppStatusStopping)
			r.dispatcher.CancelApp(app.Name, workers.CancelExternalStop, workers.OpStop)
			containerName := r.docker.ContainerName(app.Name)
			plans = append(plans, r.makeStopPlan(app.Name, containerName, r.resolveStopTimeout(app.Def.StopTimeout)))
		case AppStatusStarting:
			app.desiredNext = ""
			app.stoppingStartedAt = now
			app.initialSweep = false
			r.setAppStatus(app, AppStatusStopping)
			r.dispatcher.CancelApp(app.Name, workers.CancelExternalStop, workers.OpStop)
			var containerName string
			if app.ContainerID != "" {
				// T20 — main container already created; stop by app container name.
				containerName = r.docker.ContainerName(app.Name)
			} else {
				// T20c — init phase; the init worker was just canceled, but
				// the init container may still be running. Target its name.
				containerName = r.docker.ContainerName(app.Name + "-init")
			}
			plans = append(plans, r.makeStopPlan(app.Name, containerName, r.resolveStopTimeout(app.Def.StopTimeout)))
		case AppStatusStoppingFailed:
			// Fresh T20: re-attempt stopContainer. The new Dispatch supersedes
			// any stale slot via attemptID bump.
			app.desiredNext = ""
			app.stoppingStartedAt = now
			app.initialSweep = false
			r.setAppStatus(app, AppStatusStopping)
			containerName := r.docker.ContainerName(app.Name)
			plans = append(plans, r.makeStopPlan(app.Name, containerName, r.resolveStopTimeout(app.Def.StopTimeout)))
		case AppStatusPulling, AppStatusReadyToPull, AppStatusReadyToStart,
			AppStatusDepsWaiting, AppStatusPullFailed:
			// T20b: no container exists; cancel any pull/start worker and
			// mark STOPPED directly.
			r.dispatcher.CancelApp(app.Name, workers.CancelExternalStop)
			app.desiredNext = ""
			app.initialSweep = false
			r.setAppStatus(app, AppStatusStopped)
		case AppStatusStopping:
			// Already in flight — but if a prior RestartApp (or T17a liveness)
			// set desiredNext=READY_TO_PULL, T21 would route the app back
			// through READY_TO_PULL instead of STOPPED, stalling the
			// stop-continuation predicate (allAppsTerminalInGroup never
			// becomes true) and deadlocking Shutdown. Clear desiredNext so
			// T21 routes directly to STOPPED. Operator shutdown intent
			// overrides any pending restart intent.
			app.desiredNext = ""
		case AppStatusStopped:
			// Already terminal — skip.
		}
	}
	return plans
}

// doDetach exits runtimeLoop without touching containers. Used for binary
// upgrades — the daemon process exits but the platform keeps running, and
// the next daemon attaches to existing containers via doStart's hash-matching
// path (buildExistingContainerMap → reuse).
//
// The current namespace status is preserved (typically RUNNING) so the next
// daemon's status recovery (server.go) auto-starts the namespace, which then
// reuses the live containers instead of recreating them.
//
// Flow:
//  1. Cancel runCtx (kills reconciler + goroutines scoped to runCtx).
//  2. r.dispatcher.CancelAll(CancelDetach) — every in-flight worker canceled.
//  3. Poll ActiveWorkers() with a 5s cap; log if exceeded.
//  4. Persist state under Lock.
//  5. signalShutdown() — closes shutdownComplete so runtimeLoop exits.
//
// Canceled Results with res.Err != nil are dropped silently by
// applyWorkerResult (CancelDetach branch). Non-error Results proceed to their
// handlers — a successful Result on a detaching runtime still corresponds to
// a real Docker state change (e.g. a pull that finished before cancel
// observed ctx.Done).
func (r *Runtime) doDetach() {
	// Fence stepAllAppsUnderLock / tickUnderLock against dispatching new
	// workers on the detaching runtime. The current iteration still runs its
	// tail (stepAllApps / tick / ...) after applyCommand returns for
	// cmdDetach; without this, pre-RUNNING apps (READY_TO_PULL, DEPS_WAITING,
	// START_FAILED, PULL_FAILED) could spawn pull / start / liveness /
	// reconcile workers that survive detach. Set BEFORE CancelAll so the
	// guard is visible by the time the loop tail reaches the checks.
	r.detaching.Store(true)

	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()

	// Cancel all in-flight workers with reason=Detach. Their ctx.Err-flavored
	// Results land in resultCh but runtimeLoop will exit before draining —
	// they're dropped silently by the buffered channel when the loop returns.
	canceled := r.dispatcher.CancelAll(workers.CancelDetach)
	if canceled > 0 {
		slog.Debug("Detach: canceled in-flight workers", "count", canceled)
	}

	// Poll for workers to finish. r.wg tracks BOTH the runtimeLoop goroutine
	// (Add(1) in Start) AND dispatcher workers (via dispatcher.workerWg=&r.wg),
	// so r.wg.Wait() would deadlock here — the loop is still running inside
	// this detach handler. Instead, poll Dispatcher.ActiveWorkers which
	// decrements when each worker's fn returns (sendResult either succeeds
	// via the 128-cap resultCh or is dropped on parentCtx.Done — neither
	// blocks the worker goroutine). 5s cap.
	deadline := time.Now().Add(5 * time.Second)
	for r.dispatcher.ActiveWorkers() > 0 {
		if time.Now().After(deadline) {
			slog.Warn("Detach: workers did not exit within 5s; proceeding",
				"pending", r.dispatcher.ActiveWorkers())
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Drain legacy wait group (appWg). Belt-and-suspenders for any goroutines
	// that appWg.Add'd without going through the dispatcher.
	r.appWg.Wait()

	r.mu.Lock()
	// Final persist on detach: runtimeLoop is about to exit, so the dirty-flag
	// tail will not run again. Persist inline + clear r.dirty.
	r.persistState()
	r.dirty.Store(false)
	leftRunning := len(r.apps)
	r.mu.Unlock()

	if r.history != nil {
		r.history.Record("detach", "", "success", 0, nil, leftRunning)
	}
	slog.Info("Runtime detached, containers left running for next daemon to adopt", "count", leftRunning)

	// Signal shutdownComplete so runtimeLoop exits on its next iteration.
	// Idempotent via sync.Once — safe if a prior Stop-path continuation
	// already signaled.
	r.signalShutdown()
}

// refreshSnapshotDigests pulls :snapshot images from the registry in parallel
// so subsequent GetImageDigest calls return the freshly-fetched digest. This
// ensures reload/start catches dev pushes that reuse the same :snapshot tag
// without a version bump — otherwise the hash-diff would compute against a
// stale local digest and keep the old container running.
//
// Scope: only fires for apps whose image tag matches shouldPullImage (today:
// tag contains "snapshot", case-insensitive; ThirdParty apps excluded).
// Pinned release tags are never pulled here, so production reload is unaffected.
//
// Failure policy: best-effort. A pull failure (network, auth, registry down)
// is logged at WARN and the flow falls back to the cached local digest. If
// the image is missing entirely, the downstream state-machine pull dispatch
// (T2) will surface PULL_FAILED via T6. No code path here is fatal.
//
// Concurrency: bounded to 4 parallel pulls; per-pull ctx timeout 2 minutes
// (snapshot images are typically small; an initial community deploy would
// be ~60s per app, so 2m is safely inside the budget).
func (r *Runtime) refreshSnapshotDigests(ctx context.Context, apps []appdef.ApplicationDef) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, def := range apps {
		if def.Image == "" || !shouldPullImage(def.Kind, def.Image) {
			continue
		}
		wg.Add(1)
		go func(appName, image string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pullCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			auth := r.registryAuth(image)
			if err := r.docker.PullImageWithProgress(pullCtx, image, auth, nil); err != nil {
				slog.Warn("pre-flight snapshot pull failed; falling back to cached local digest",
					"app", appName, "image", image, "err", err)
			}
		}(def.Name, def.Image)
	}
	wg.Wait()
}
