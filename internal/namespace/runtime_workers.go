// Package namespace — worker TaskFunc factories.
//
// Each factory returns a dispatchPlan whose taskID is (appName, op) and whose
// TaskFunc performs Docker I/O + retry loops.
//
// Concurrency contract:
//   - Workers receive value-copied inputs only. They must NOT read r.apps or
//     mutate AppRuntime fields. They may call r.docker.* and
//     r.registryAuthFn.Load() (atomic).
//   - Workers must NOT emit events. All status transitions and event
//     buffering happen in runtimeLoop via applyWorkerResult.
package namespace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/nsactions"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
	dockerpkg "github.com/docker/docker/client"
)

// pullStallTimeout bounds the no-progress window for a single
// PullImageWithProgress invocation. If no progress callback fires for this
// long, the pull's per-attempt context is canceled and the retry loop either
// advances to the next delay slot, surfaces an auth-error, or falls back to
// the local image (see runPullTask).
//
// Declared as a var (not const) so tests can override it without build flags.
var pullStallTimeout = 5 * time.Minute

// pullStallPollInterval is the watchdog tick cadence. Chosen small enough to
// keep the stall-detection latency tight but not so small as to produce
// excessive wakeups on long pulls.
//
// Declared as a var (not const) so tests can override it without build flags.
var pullStallPollInterval = 30 * time.Second

// pullWorkLabel tags pull TaskFunc goroutines for runtime/pprof inspection.
//
//nolint:gosec // G101 false positive — these are pprof label strings, not secrets.
const (
	pullWorkLabel             = "citeck-runtime-pull"
	startWorkLabel            = "citeck-runtime-start"
	stopWorkLabel             = "citeck-runtime-stop"
	initContainerLabel        = "citeck-runtime-init"
	startupProbeWorkLabel     = "citeck-runtime-startup-probe"
	removeNetworkWorkLabel    = "citeck-runtime-remove-network"
	reconcileDiffWorkLabel    = "citeck-runtime-reconcile-diff"
	livenessProbeWorkLabel    = "citeck-runtime-liveness-probe"
	postStartActionsWorkLabel = "citeck-runtime-post-start-actions"
)

// postStartActionsTimeout bounds the total time for the chain of post-start
// exec actions. Matches the legacy runPostStartInitActions inline timeout —
// a hung action (e.g. postgres CREATE DATABASE on a slow disk) used to block
// runtimeLoop itself; hoisted into a worker it only ties up a dispatcher slot.
const postStartActionsTimeout = 5 * time.Minute

// reconcileInspectTimeout bounds docker inspect calls during reconcile-diff
// OOM detection; matches the legacy reconciler.go.
const reconcileInspectTimeout = 5 * time.Second

// removeNetworkTimeout bounds the Docker RemoveNetwork call; matches the
// legacy doStop post-loop timeout.
const removeNetworkTimeout = 10 * time.Second

// initContainerExitTimeout bounds WaitForContainerExit for init containers.
const initContainerExitTimeout = 60 * time.Second

// makePullPlan returns a dispatchPlan that pulls image for appName, honoring
// nsactions.PullRetryDelays.
//
// Short-circuit path: when pullAlways == false AND the image already exists
// locally, the TaskFunc returns Result{Payload: PullPayload{Digest: X}}
// without calling PullImageWithProgress. X is the current local digest.
//
// On 401/403-style errors, the returned error is wrapped with an actionable
// "docker login <host>" hint.
//
// Retry budget: up to (nsactions.PullRetriesForExistingImage + 1) pull
// invocations (currently 4) before the local-image fallback path activates.
func (r *Runtime) makePullPlan(appName, image string, pullAlways bool, progressFn docker.PullProgressFn) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpPull},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", pullWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runPullTask(ctx, image, pullAlways, nsactions.PullRetryDelays, progressFn)
			})
			return result
		},
	}
}

// runPullTask is the shared pull retry loop used by makePullPlan AND
// makeInitContainerPlan. retryDelays controls the between-attempt sleeps
// (nsactions.PullRetryDelays for normal apps; nsactions.InitPullRetryDelays
// for init containers).
func (r *Runtime) runPullTask(
	ctx context.Context,
	image string,
	pullAlways bool,
	retryDelays []time.Duration,
	progressFn docker.PullProgressFn,
) workers.Result {
	// Short-circuit: image already present and caller didn't demand a re-pull.
	if !pullAlways && r.docker.ImageExists(ctx, image) {
		digest := r.docker.GetImageDigest(ctx, image)
		return workers.Result{Payload: workers.PullPayload{Digest: digest}}
	}

	auth := r.registryAuth(image)

	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return workers.Result{Err: fmt.Errorf("pull %s: %w", image, err)}
		}

		// Per-attempt stall watchdog: wrap progressFn so each callback bumps
		// lastProgress. If no progress arrives within pullStallTimeout, the
		// watchdog cancels stallCtx to unstick the pull.
		stallCtx, stallCancel := context.WithCancel(ctx)
		var lastProgress atomic.Int64
		lastProgress.Store(time.Now().UnixNano())
		wrappedProgressFn := func(a, b float64, pct int) {
			lastProgress.Store(time.Now().UnixNano())
			if progressFn != nil {
				progressFn(a, b, pct)
			}
		}
		stalled := make(chan struct{})
		watchdogDone := make(chan struct{})
		go func() {
			defer close(watchdogDone)
			ticker := time.NewTicker(pullStallPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stallCtx.Done():
					return
				case <-ticker.C:
					// Direct int64 nanosecond diff — avoids the extra time.Unix(0, n) +
					// time.Since allocation/conversion on the hot watchdog path. Still
					// wall-clock based (Go exposes no monotonic clock value for atomic
					// storage); NTP skew during a 5-min pull window is negligible in
					// practice.
					elapsedNs := time.Now().UnixNano() - lastProgress.Load()
					if elapsedNs > int64(pullStallTimeout) {
						slog.Warn("Pull stalled; canceling attempt",
							"image", image, "elapsedMs", elapsedNs/1e6)
						close(stalled)
						stallCancel()
						return
					}
				}
			}
		}()

		pullErr := r.docker.PullImageWithProgress(stallCtx, image, auth, wrappedProgressFn)
		stallCancel()
		<-watchdogDone

		// Detect the stall-cancellation path: stallCtx was canceled but the
		// outer ctx is still alive. Surface as a clear stall error (instead of
		// a generic context.Canceled) so the retry-budget branch below sees a
		// non-auth, non-ctx-err and either retries or falls back to local.
		// Guard: only overwrite an existing error — if PullImageWithProgress
		// returned nil (success) and the watchdog fired simultaneously, the
		// stall channel may be closed but the pull genuinely succeeded.
		select {
		case <-stalled:
			if pullErr != nil && ctx.Err() == nil {
				pullErr = fmt.Errorf("no progress for %v", pullStallTimeout)
			}
		default:
		}

		if pullErr == nil {
			// Success: fetch digest for deployment-hash tracking. If it
			// fails (transient API issue), return an empty Digest — the hash
			// remains unchanged, matching legacy behavior.
			digest := r.docker.GetImageDigest(ctx, image)
			return workers.Result{Payload: workers.PullPayload{Digest: digest}}
		}
		lastErr = pullErr

		// Per-attempt fallback: when this attempt is at or beyond the
		// PullRetriesForExistingImage threshold AND the image exists locally,
		// accept that as success — 4 pull invocations before local-image fallback.
		if attempt >= nsactions.PullRetriesForExistingImage && r.docker.ImageExists(ctx, image) {
			digest := r.docker.GetImageDigest(ctx, image)
			return workers.Result{Payload: workers.PullPayload{Digest: digest}}
		}

		// Auth errors are not retryable — surface immediately with the
		// actionable "docker login" hint.
		if nsactions.IsAuthError(pullErr) {
			return workers.Result{Err: fmt.Errorf(
				"pull %s: authentication failed — run: docker login %s",
				image, nsactions.RegistryHost(image),
			)}
		}

		// Exhausted retry budget.
		if attempt >= len(retryDelays) {
			return workers.Result{Err: fmt.Errorf("pull %s: %w", image, lastErr)}
		}

		// Wait before next attempt; a cancel during the wait aborts promptly.
		// gosec G602 false positive: taint analysis doesn't track through the
		// var-scoped `pullStall*` knobs in this function. Confirmed empirically
		// by flipping the knobs to const (clean) vs var (G602 fires) on
		// identical code. The `attempt >= len(retryDelays)` guard directly
		// above this select proves bounds safety.
		retryTimer := time.NewTimer(retryDelays[attempt]) //nolint:gosec // see comment above — bounds checked by the len guard directly above
		select {
		case <-ctx.Done():
			retryTimer.Stop()
			return workers.Result{Err: fmt.Errorf("pull %s: %w", image, ctx.Err())}
		case <-retryTimer.C:
		}
	}
}

// makeStartPlan returns a dispatchPlan that creates+starts a container for
// appDef under appName, honoring nsactions.ContainerCreateRetries.
//
// On success: Payload = StartPayload{ContainerID: id}. On failure: Err set.
//
// The TaskFunc first removes any stale container (best-effort, error ignored)
// then retries CreateContainer up to ContainerCreateRetries times.
// StartContainer is NOT retried — an error there is terminal.
func (r *Runtime) makeStartPlan(appName string, appDef appdef.ApplicationDef, volumesBase string) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpStart},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", startWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runStartTask(ctx, appName, appDef, volumesBase)
			})
			return result
		},
	}
}

func (r *Runtime) runStartTask(
	ctx context.Context,
	appName string,
	appDef appdef.ApplicationDef,
	volumesBase string,
) workers.Result {
	containerName := r.docker.ContainerName(appName)

	// Best-effort cleanup of any stale container with the same name. Errors
	// are intentionally dropped — the container may not exist (fresh start).
	_ = r.docker.StopAndRemoveContainer(ctx, containerName, 0)

	var (
		id      string
		lastErr error
	)
	for range nsactions.ContainerCreateRetries {
		if err := ctx.Err(); err != nil {
			return workers.Result{Err: fmt.Errorf("create container %s: %w", appName, err)}
		}

		createdID, err := r.docker.CreateContainer(ctx, appDef, volumesBase)
		if err == nil {
			id = createdID
			lastErr = nil
			break
		}
		lastErr = err

		// Wait before next attempt; a cancel during the wait aborts promptly.
		retryTimer := time.NewTimer(nsactions.ContainerCreateRetryDelay)
		select {
		case <-ctx.Done():
			retryTimer.Stop()
			return workers.Result{Err: fmt.Errorf("create container %s: %w", appName, ctx.Err())}
		case <-retryTimer.C:
		}
	}
	if lastErr != nil {
		return workers.Result{Err: fmt.Errorf("create container %s: %w", appName, lastErr)}
	}

	if err := r.docker.StartContainer(ctx, id); err != nil {
		return workers.Result{Err: fmt.Errorf("start container %s: %w", appName, err)}
	}

	return workers.Result{Payload: workers.StartPayload{ContainerID: id}}
}

// makeStopPlan returns a dispatchPlan that stops+removes the container named
// containerName for appName, with a 2-retry loop (1s delay).
//
// stopTimeout is the docker stop timeout in seconds (0 = Docker default).
func (r *Runtime) makeStopPlan(appName, containerName string, stopTimeout int) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpStop},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", stopWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runStopTask(ctx, appName, containerName, stopTimeout)
			})
			return result
		},
	}
}

func (r *Runtime) runStopTask(ctx context.Context, appName, containerName string, stopTimeout int) workers.Result {
	const maxAttempts = 3 // attempt 0 + 2 retries
	var lastErr error
	for range maxAttempts {
		if err := ctx.Err(); err != nil {
			return workers.Result{Err: fmt.Errorf("stop %s: %w", appName, err)}
		}
		err := r.docker.StopAndRemoveContainer(ctx, containerName, stopTimeout)
		if err == nil {
			return workers.Result{Payload: workers.StopPayload{}}
		}
		lastErr = err
		retryTimer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			retryTimer.Stop()
			return workers.Result{Err: fmt.Errorf("stop %s: %w", appName, ctx.Err())}
		case <-retryTimer.C:
		}
	}
	return workers.Result{Err: fmt.Errorf("stop %s: %w", appName, lastErr)}
}

// makeInitContainerPlan returns a dispatchPlan for running a single init
// container:
//
//  1. Pull the init image (retry delays = nsactions.InitPullRetryDelays).
//  2. Stop+remove any stale "{appName}-init" container (best-effort).
//  3. CreateContainer + StartContainer for the init container.
//  4. WaitForContainerExit (60s timeout).
//  5. Remove the init container regardless of outcome (cleanup).
//
// Callers that need to run a chain of init containers dispatch one plan per
// init container and chain Results via applyWorkerResult.
//
// stepIdx is the 0-based index in appDef.InitContainers. It is stamped onto
// the success Payload (workers.InitPayload.Index) so applyWorkerResult can
// decide whether to dispatch the next init (T11) or start the container (T12).
// On error the Payload is left zero — T10 routes terminally.
func (r *Runtime) makeInitContainerPlan(
	appName, image string,
	stepIdx int,
	initDef appdef.ApplicationDef,
	volumesBase string,
) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpInit},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", initContainerLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runInitContainerTask(ctx, appName, image, initDef, volumesBase)
				if result.Err == nil {
					// Stamp success Payload with the chain index so the
					// state machine can route T11 (next) vs T12 (last).
					result.Payload = workers.InitPayload{Index: stepIdx}
				}
			})
			return result
		},
	}
}

func (r *Runtime) runInitContainerTask(
	ctx context.Context,
	appName, image string,
	initDef appdef.ApplicationDef,
	volumesBase string,
) workers.Result {
	// Step 1: pull the init image (shorter retry delays).
	pullRes := r.runPullTask(ctx, image, false, nsactions.InitPullRetryDelays, nil)
	if pullRes.Err != nil {
		return workers.Result{Err: fmt.Errorf("pull init image %s: %w", image, pullRes.Err)}
	}

	// Step 2: stop+remove any stale init container (best-effort).
	initName := r.docker.ContainerName(appName + "-init")
	_ = r.docker.StopAndRemoveContainer(ctx, initName, 0)

	// Step 3: create.
	initID, err := r.docker.CreateContainer(ctx, initDef, volumesBase)
	if err != nil {
		return workers.Result{Err: fmt.Errorf("create init container for %s: %w", appName, err)}
	}

	// After create, cleanup always runs on exit. Captured via closure so the
	// happy-path remove and the error-path remove share the same code.
	var removed bool
	cleanup := func() {
		if removed {
			return
		}
		removed = true
		// Use a detached context with timeout to ensure cleanup happens even
		// if the task's ctx has been canceled — matches runInitContainers.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.docker.RemoveContainer(cleanupCtx, initID)
		cleanupCancel()
	}

	// Step 4: start.
	if startErr := r.docker.StartContainer(ctx, initID); startErr != nil {
		cleanup()
		return workers.Result{Err: fmt.Errorf("start init container for %s: %w", appName, startErr)}
	}

	// Step 5: wait for exit.
	if waitErr := r.docker.WaitForContainerExit(ctx, initID, initContainerExitTimeout); waitErr != nil {
		cleanup()
		// Preserve ctx.Err passthrough if the caller canceled mid-wait.
		if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
			return workers.Result{Err: fmt.Errorf("init container for %s: %w", appName, waitErr)}
		}
		return workers.Result{Err: fmt.Errorf("init container exited with error for %s: %w", appName, waitErr)}
	}

	cleanup()
	return workers.Result{Payload: workers.InitPayload{}}
}

// makeRemoveNetworkPlan returns a dispatchPlan that removes the namespace's
// Docker network. Dispatched by the cmdStopNextGroup continuation chain once
// all graceful-shutdown groups have drained to terminal stop states. The
// TaskID uses "" as App name — network removal is namespace-scoped, not
// per-app, and no other worker kind competes for this slot.
func (r *Runtime) makeRemoveNetworkPlan() dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: "", Op: workers.OpRemoveNetwork},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", removeNetworkWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				netCtx, cancel := context.WithTimeout(ctx, removeNetworkTimeout)
				defer cancel()
				if err := r.docker.RemoveNetwork(netCtx); err != nil {
					result = workers.Result{Err: err}
					return
				}
				result = workers.Result{Payload: workers.RemoveNetworkPayload{}}
			})
			return result
		},
	}
}

// reconcileSnapshotEntry is the minimal per-app input passed to a
// ReconcileDiffTask worker. Captured under r.mu.Lock at dispatch time so the
// worker itself never reads r.apps (worker isolation).
type reconcileSnapshotEntry struct {
	Name        string
	ContainerID string
}

// makeReconcileDiffPlan returns a dispatchPlan that queries Docker for the
// namespace's running containers and reports which apps in snapshot have gone
// missing (their container is no longer in the running-set). For each missing
// app, the worker inspects its ContainerID outside any lock to detect OOM
// kills. Results flow into handleReconcileDiffResult which applies T18.
//
// Worker isolation: the worker receives snapshot by value and only touches
// r.docker.*. It does not read r.apps or any other mutable state.
func (r *Runtime) makeReconcileDiffPlan(snapshot []reconcileSnapshotEntry) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: "", Op: workers.OpReconcileDiff},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", reconcileDiffWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runReconcileDiffTask(ctx, snapshot)
			})
			return result
		},
	}
}

func (r *Runtime) runReconcileDiffTask(ctx context.Context, snapshot []reconcileSnapshotEntry) workers.Result {
	containers, err := r.docker.GetContainers(ctx)
	if err != nil {
		// Return the error so the handler can log it and skip this cycle. The
		// state machine does not transition anything on a failed diff.
		if dockerpkg.IsErrConnectionFailed(err) {
			return workers.Result{Err: fmt.Errorf("reconcile-diff: docker unreachable: %w", err)}
		}
		return workers.Result{Err: fmt.Errorf("reconcile-diff: list containers: %w", err)}
	}

	runningByApp := make(map[string]bool, len(containers))
	for _, c := range containers {
		if appName, ok := c.Labels[docker.LabelAppName]; ok {
			if c.State == "running" {
				runningByApp[appName] = true
			}
		}
	}

	var missing []workers.MissingApp
	for _, entry := range snapshot {
		if runningByApp[entry.Name] {
			continue
		}
		m := workers.MissingApp{Name: entry.Name, ContainerID: entry.ContainerID}
		if entry.ContainerID != "" {
			inspCtx, cancel := context.WithTimeout(ctx, reconcileInspectTimeout)
			info, inspErr := r.docker.InspectContainer(inspCtx, entry.ContainerID)
			cancel()
			if inspErr == nil && info.State != nil && info.State.OOMKilled {
				m.OOMKilled = true
			}
		}
		missing = append(missing, m)
	}

	return workers.Result{Payload: workers.ReconcileDiffPayload{Missing: missing}}
}

// makeLivenessProbePlan returns a dispatchPlan that executes one liveness
// probe (HTTP or exec) against containerID. Payload.Healthy reports the
// outcome. Non-nil Err is treated as unhealthy by the handler (see
// handleLivenessProbeResult).
//
// Worker isolation: the worker only calls r.docker.* and r.runLivenessProbe (a
// pure function of its arguments + r.docker). No access to r.apps.
func (r *Runtime) makeLivenessProbePlan(appName, containerID string, probe *appdef.AppProbeDef) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpLivenessProbe},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", livenessProbeWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				healthy := r.runLivenessProbe(ctx, containerID, probe)
				result = workers.Result{Payload: workers.LivenessProbePayload{Healthy: healthy}}
			})
			return result
		},
	}
}

// makeStartupProbePlan returns a dispatchPlan that runs the startup-condition
// checks (log pattern + probe) for containerID. r.waitForStartup only reads
// from r.docker.* and local state — worker isolation preserved.
func (r *Runtime) makeStartupProbePlan(
	appName, containerID string,
	conditions []appdef.StartupCondition,
) dispatchPlan {
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpProbe},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", startupProbeWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				if err := r.waitForStartup(ctx, appName, containerID, conditions); err != nil {
					result = workers.Result{Err: err}
					return
				}
				result = workers.Result{Payload: workers.ProbePayload{}}
			})
			return result
		},
	}
}

// makePostStartActionsPlan returns a dispatchPlan that executes the chain of
// post-start init actions (action.Exec) for containerID. Dispatched from the
// T15/T16 RUNNING transitions so runtimeLoop is not blocked by slow execs
// (e.g. postgres CREATE DATABASE on a cold disk). Actions are best-effort —
// errors and non-zero exits are logged, not fatal.
//
// Worker isolation: actions are captured by value at dispatch time; the worker
// only touches r.docker.ExecInContainer. containerID is captured at dispatch —
// if the container is later recreated, a stale ExecInContainer will fail
// transiently; the state machine is unaffected because RUNNING is already
// committed.
func (r *Runtime) makePostStartActionsPlan(
	appName, containerID string,
	actions []appdef.AppInitAction,
) dispatchPlan {
	// Copy actions by value so the worker can't observe a mutated AppDef.
	actionsCopy := make([]appdef.AppInitAction, len(actions))
	copy(actionsCopy, actions)
	return dispatchPlan{
		taskID: workers.TaskID{App: appName, Op: workers.OpPostStartActions},
		fn: func(ctx context.Context) workers.Result {
			labels := pprof.Labels("work", postStartActionsWorkLabel)
			var result workers.Result
			pprof.Do(ctx, labels, func(ctx context.Context) {
				result = r.runPostStartActionsTask(ctx, appName, containerID, actionsCopy)
			})
			return result
		},
	}
}

func (r *Runtime) runPostStartActionsTask(
	ctx context.Context,
	appName, containerID string,
	actions []appdef.AppInitAction,
) workers.Result {
	if len(actions) == 0 || containerID == "" {
		return workers.Result{Payload: workers.PostStartActionsPayload{}}
	}
	actCtx, cancel := context.WithTimeout(ctx, postStartActionsTimeout)
	defer cancel()
	for i, action := range actions {
		if len(action.Exec) == 0 {
			continue
		}
		if err := actCtx.Err(); err != nil {
			// Respect cancellation/timeout — stop running further actions.
			slog.Warn("Post-start actions aborted",
				"app", appName, "err", err, "pending", len(actions)-i)
			return workers.Result{Err: err}
		}
		slog.Info("Running init action", "app", appName, "cmd", action.Exec)
		output, exitCode, execErr := r.docker.ExecInContainer(actCtx, containerID, action.Exec)
		if execErr != nil {
			slog.Warn("Init action exec error", "app", appName, "cmd", action.Exec, "err", execErr)
			continue
		}
		if exitCode != 0 {
			slog.Warn("Init action exited with non-zero code",
				"app", appName, "cmd", action.Exec, "exitCode", exitCode, "output", output)
		}
	}
	return workers.Result{Payload: workers.PostStartActionsPayload{}}
}
