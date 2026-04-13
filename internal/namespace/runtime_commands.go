package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Start begins the namespace lifecycle with the given app definitions.
func (r *Runtime) Start(apps []appdef.ApplicationDef) {
	if !r.running.CompareAndSwap(false, true) {
		slog.Warn("Runtime already running, ignoring Start()")
		return
	}
	r.wg.Add(1)
	go r.runLoop()
	select {
	case r.cmdCh <- command{typ: cmdStart, apps: apps}:
	default:
		slog.Warn("Start command dropped (channel full)")
	}
}

// Stop signals the runtime to begin shutting down.
func (r *Runtime) Stop() {
	select {
	case r.stopCh <- struct{}{}:
	default:
		// Already signaled
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
// return value and surface a clear error to the user.
func (r *Runtime) Detach() bool {
	r.mu.RLock()
	stopInFlight := r.status == NsStatusStopping
	r.mu.RUnlock()
	if stopInFlight {
		return false
	}
	select {
	case r.detachCh <- struct{}{}:
	default:
		// Already signaled
	}
	return true
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
// shutdownOnce wins, so concurrent Shutdown/ShutdownDetached invocations
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
	r.shutdownOnce.Do(func() {
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
		r.reconcileWg.Wait()
		close(r.eventCh) // stops dispatchLoop
		if r.ownsActions {
			r.actionSvc.Shutdown()
		}
	})
}

// Regenerate sends a regeneration command to the runtime loop.
// If cfg is non-nil, the runtime config is atomically updated before regenerating.
// If bundleDef is non-nil, it is persisted as the cached bundle for fallback on future resolve failures.
func (r *Runtime) Regenerate(apps []appdef.ApplicationDef, cfg *Config, bundleDef *bundle.Def) {
	select {
	case r.cmdCh <- command{typ: cmdRegenerate, apps: apps, cfg: cfg, bundleDef: bundleDef}:
	default:
		slog.Warn("Regenerate command dropped (channel full)")
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
	r.persistState()
	return nil
}

// StopApp stops a single app by name and marks it as detached.
// Detached apps are excluded from namespace start/reload/regenerate.
// Use StartApp to re-attach.
func (r *Runtime) StopApp(appName string) error {
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
	containerID := app.ContainerID
	r.mu.Unlock()

	// Blocking Docker call outside the lock — 2 minute timeout to prevent indefinite hang
	if containerID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := r.docker.StopAndRemoveContainer(ctx, containerName, 0); err != nil {
			slog.Warn("StopApp: container stop failed (may already be gone)", "app", appName, "err", err)
		}
	}

	r.mu.Lock()
	// Re-lookup app after releasing lock — the map may have changed during the Docker call
	if app, ok = r.apps[appName]; ok {
		app.ContainerID = ""
		r.setAppStatus(app, AppStatusStopped)
	}
	r.persistState()
	r.mu.Unlock()
	return nil
}

// StartApp starts a single app that is not currently running.
// Unlike RestartApp, it handles never-started apps (READY_TO_PULL, PULL_FAILED, START_FAILED).
func (r *Runtime) StartApp(appName string) error {
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}
	switch app.Status {
	case AppStatusRunning:
		r.mu.Unlock()
		return nil // already running
	case AppStatusReadyToPull, AppStatusPullFailed, AppStatusStartFailed, AppStatusStopped, AppStatusFailed:
		// These states can be re-entered via pullAndStartApp
		ctx := r.runCtx
		if ctx == nil {
			r.mu.Unlock()
			return fmt.Errorf("runtime not started, cannot start app %q", appName)
		}
		r.setAppStatus(app, AppStatusPulling)
		delete(r.manualStoppedApps, appName)
		r.resetRetry(appName)
		r.persistState()
		r.mu.Unlock()
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, appName)
		return nil
	default:
		r.mu.Unlock()
		// For other states (PULLING, STARTING, etc.), delegate to RestartApp
		return r.RestartApp(appName)
	}
}

// RetryPullFailedApps re-queues all apps in PULL_FAILED state for pull+start.
// Called after secrets change so that apps that failed due to missing auth can recover.
func (r *Runtime) RetryPullFailedApps() int {
	r.mu.Lock()
	ctx := r.runCtx
	if ctx == nil {
		r.mu.Unlock()
		return 0
	}
	var retried int
	for _, app := range r.apps {
		if app.Status != AppStatusPullFailed {
			continue
		}
		r.setAppStatus(app, AppStatusPulling)
		r.resetRetry(app.Name)
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, app.Name)
		retried++
	}
	r.mu.Unlock()
	return retried
}

// RestartApp stops and re-starts a single app.
func (r *Runtime) RestartApp(appName string) error {
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}
	containerName := r.docker.ContainerName(appName)
	hasContainer := app.ContainerID != ""
	r.mu.Unlock()

	// Use a fresh timeout context for the Docker stop — not runCtx which gets
	// canceled on shutdown (would abort the stop mid-way).
	r.mu.RLock()
	running := r.runCtx != nil
	r.mu.RUnlock()
	if !running {
		return fmt.Errorf("runtime not started, cannot restart app %q", appName)
	}
	if hasContainer {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_ = r.docker.StopAndRemoveContainer(stopCtx, containerName, 0)
		stopCancel()
	}

	r.mu.Lock()
	if app, ok = r.apps[appName]; ok {
		app.ContainerID = ""
		r.setAppStatus(app, AppStatusStarting)
	}
	delete(r.manualStoppedApps, appName)
	r.resetRetry(appName)
	needsRegen := r.dependsOnDetachedApps[appName]
	r.mu.Unlock()

	// If this detached app is a dependency of other apps, trigger full regeneration
	// so that dependents (e.g. proxy) get reconfigured with the dependency back
	if needsRegen {
		slog.Info("Restarting detached app triggers regeneration", "app", appName)
		r.mu.RLock()
		regenApps := r.lastApps
		r.mu.RUnlock()
		select {
		case r.cmdCh <- command{typ: cmdRegenerate, apps: regenApps}:
		default:
		}
		return nil
	}

	// Re-start in background using runCtx for the new container lifecycle
	r.mu.RLock()
	runCtx := r.runCtx
	r.mu.RUnlock()
	r.appWg.Add(1)
	go r.restartApp(runCtx, appName)
	return nil
}

func (r *Runtime) restartApp(ctx context.Context, appName string) {
	defer r.appWg.Done()

	r.mu.RLock()
	app := r.apps[appName]
	if app == nil {
		r.mu.RUnlock()
		slog.Warn("restartApp: app not found (possibly stopped concurrently)", "app", appName)
		return
	}
	appDef := app.Def
	r.mu.RUnlock()

	// Stop phase uses independent context so daemon shutdown doesn't interrupt the stop call
	containerName := r.docker.ContainerName(appName)
	stopTimeout := appDef.StopTimeout
	if stopTimeout == 0 {
		stopTimeout = r.defaultStopTimeout
	}
	if stopTimeout == 0 {
		stopTimeout = 15
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Duration(stopTimeout+5)*time.Second)
	_ = r.docker.StopAndRemoveContainer(stopCtx, containerName, stopTimeout)
	stopCancel()

	id, err := r.docker.CreateContainer(ctx, appDef, r.volumesBase)
	if err != nil {
		slog.Error("Restart create failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	if err := r.docker.StartContainer(ctx, id); err != nil {
		slog.Error("Restart start failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	app.ContainerID = id
	r.mu.Unlock()

	// Wait for startup probe
	if len(appDef.StartupConditions) > 0 {
		if err := r.waitForStartup(ctx, appName, id, appDef.StartupConditions); err != nil {
			slog.Error("Restart probe failed", "app", appName, "err", err)
			r.mu.Lock()
			r.setAppStatus(app, AppStatusStartFailed)
			app.StatusText = err.Error()
			r.mu.Unlock()
			return
		}
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusRunning)
	r.mu.Unlock()
}
