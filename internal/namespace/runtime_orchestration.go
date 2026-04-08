package namespace

import (
	"context"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

func (r *Runtime) runLoop() {
	defer r.wg.Done()
	defer r.running.Store(false) // allow Start() to be called again after stop
	slog.Info("Namespace runtime thread started", "namespace", r.nsID)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case cmd := <-r.cmdCh:
			switch cmd.typ {
			case cmdStart:
				r.doStart(cmd.apps)
			case cmdRegenerate:
				if cmd.cfg != nil || cmd.bundleDef != nil {
					r.mu.Lock()
					if cmd.cfg != nil {
						r.config = cmd.cfg
					}
					if cmd.bundleDef != nil && !cmd.bundleDef.IsEmpty() {
						r.cachedBundle = cmd.bundleDef
					}
					r.mu.Unlock()
				}
				r.doRegenerate(cmd.apps)
			}
		case <-r.stopCh:
			r.doStop()
			return
		case <-ticker.C:
			if r.statsRunning.CompareAndSwap(false, true) {
				go func() {
					defer r.statsRunning.Store(false)
					r.updateStats()
				}()
			}
			r.mu.Lock()
			r.checkStatus()
			r.mu.Unlock()
		}
	}
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

	// Phase 1 (no lock): resolve image digests and compute hashes.
	// This avoids holding the mutex during Docker API calls.
	r.mu.RLock()
	editedLocked := make(map[string]bool, len(r.editedLockedApps))
	editedApps := make(map[string]appdef.ApplicationDef, len(r.editedApps))
	maps.Copy(editedLocked, r.editedLockedApps)
	maps.Copy(editedApps, r.editedApps)
	r.mu.RUnlock()

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
		// Resolve image digest from local Docker cache (no lock needed)
		if appDef.ImageDigest == "" {
			if digest := r.docker.GetImageDigest(ctx, appDef.Image); digest != "" {
				appDef.ImageDigest = digest
			}
		}
		hash := appDef.GetHash()
		containerName := r.docker.ContainerName(appDef.Name)

		plan := appPlan{def: appDef, hash: hash, containerName: containerName}
		if existing, ok := existingContainers[appDef.Name]; ok && existing.hash == hash && existing.running {
			plan.reuse = true
			plan.containerID = existing.containerID
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

	// Phase 2 (no lock): remove stale containers in parallel, wait for completion.
	var removeWg sync.WaitGroup
	for _, p := range plans {
		if !p.reuse {
			if _, ok := existingContainers[p.def.Name]; ok {
				slog.Info("Removing stale container", "app", p.def.Name)
				removeWg.Add(1)
				go func(name string) {
					defer removeWg.Done()
					if err := r.docker.StopAndRemoveContainer(ctx, name, 0); err != nil {
						slog.Warn("Failed to remove stale container", "name", name, "err", err)
					}
				}(p.containerName)
			}
		}
	}
	// Remove containers no longer in the desired set
	desiredNames := make(map[string]bool, len(plans))
	for _, p := range plans {
		desiredNames[p.def.Name] = true
	}
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

	// Verify reused containers are actually running (fast Docker inspect + health probe)
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
		// Run health probe on reused container to detect crashed apps
		if p.def.LivenessProbe != nil {
			probeCtx, probeCancel := context.WithTimeout(ctx, 10*time.Second)
			alive := r.runLivenessProbe(probeCtx, p.containerID, p.def.LivenessProbe)
			probeCancel()
			if !alive {
				slog.Warn("Reused container health probe failed, will recreate", "app", p.def.Name)
				plans[i].reuse = false
				plans[i].containerID = ""
			}
		}
	}

	// Phase 3 (lock): atomically replace in-memory state and launch apps.
	r.mu.Lock()
	newApps := make(map[string]*AppRuntime, len(plans))
	for _, p := range plans {
		if p.reuse {
			slog.Info("Reusing existing container (hash match)", "app", p.def.Name)
			newApps[p.def.Name] = &AppRuntime{
				Name: p.def.Name, Status: AppStatusRunning, Def: p.def,
				ContainerID: p.containerID,
			}
		} else {
			newApps[p.def.Name] = &AppRuntime{
				Name: p.def.Name, Status: AppStatusReadyToPull, Def: p.def,
			}
		}
	}
	r.apps = newApps
	for _, app := range r.apps {
		if app.Status == AppStatusReadyToPull {
			r.setAppStatus(app, AppStatusPulling)
			r.appWg.Add(1)
			go r.pullAndStartApp(ctx, app.Name)
		}
	}
	r.mu.Unlock()

	// Start reconciler using runCtx — stops automatically when namespace stops
	rcfg := DefaultReconcilerConfig()
	if r.reconcilerCfg != nil {
		rcfg = *r.reconcilerCfg
	}
	r.RunReconciler(ctx, rcfg)

	// Record start operation
	if r.history != nil {
		r.history.Record("start", "", "initiated", 0, nil, len(apps))
	}
}

// doRegenerate applies a new set of app definitions like docker-compose up:
// containers with matching hash keep running, changed ones are recreated,
// removed ones are stopped. No unnecessary restarts.
func (r *Runtime) doRegenerate(apps []appdef.ApplicationDef) {
	// 1. Cancel running goroutines (pull, start, reconciler)
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()

	// Wait for all goroutines to exit cleanly
	r.reconcileWg.Wait()
	r.appWg.Wait()

	// 2. Clear retry state (apps are preserved until doStart Phase 3 to avoid empty window)
	r.mu.Lock()
	r.retryState = nil // clean slate — regeneration resets retry counters
	r.mu.Unlock()

	// 3. Start with new definitions — doStart discovers running containers
	//    via buildExistingContainerMap and reuses those with matching hash.
	//    doStart Phase 3 atomically replaces r.apps, so Apps() never returns empty.
	r.doStart(apps)
}

func (r *Runtime) doStop() {
	r.mu.Lock()
	r.setStatus(NsStatusStopping)

	// Cancel all running goroutines
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Unlock()

	// Wait for reconciler goroutines to exit (they listen on ctx.Done)
	r.reconcileWg.Wait()

	// Wait for all app start/restart goroutines to finish.
	r.appWg.Wait()

	// Collect apps to stop and mark as STOPPING (reflects real state)
	r.mu.Lock()
	var toStop []*AppRuntime
	for _, app := range r.apps {
		if app.ContainerID != "" {
			toStop = append(toStop, app)
			r.setAppStatus(app, AppStatusStopping)
		}
	}
	r.mu.Unlock()

	// Stop in graceful order: proxy → webapps/other → keycloak → infra
	stopGroup := func(apps []*AppRuntime) {
		// Determine the max stop timeout across all apps in the group
		maxTimeout := 10 // default minimum
		for _, a := range apps {
			t := a.Def.StopTimeout
			if t == 0 {
				t = r.defaultStopTimeout
			}
			if t > maxTimeout {
				maxTimeout = t
			}
		}
		groupCtx, groupCancel := context.WithTimeout(context.Background(), time.Duration(maxTimeout+5)*time.Second)
		defer groupCancel()
		var wg sync.WaitGroup
		for _, a := range apps {
			wg.Add(1)
			go func(app *AppRuntime) {
				defer wg.Done()
				slog.Info("Stopping app", "app", app.Name)
				timeout := app.Def.StopTimeout
				if timeout == 0 {
					timeout = r.defaultStopTimeout
				}
				if err := r.docker.StopAndRemoveContainer(groupCtx, r.docker.ContainerName(app.Name), timeout); err != nil {
					slog.Warn("Failed to stop container", "app", app.Name, "err", err)
				}
			}(a)
		}
		wg.Wait()
	}
	for _, group := range GracefulShutdownGroups(toStop) {
		stopGroup(group)
	}

	// Update status under lock after all containers are stopped
	r.mu.Lock()
	for _, app := range r.apps {
		r.setAppStatus(app, AppStatusStopped)
	}
	netCtx, netCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = r.docker.RemoveNetwork(netCtx)
	netCancel()
	r.apps = make(map[string]*AppRuntime)
	// Clear restart tracking — fresh state on next Start
	r.restartCounts = make(map[string]int)
	r.restartEvents = nil
	r.livenessFailures = make(map[string]int)
	r.setStatus(NsStatusStopped) // persists state including cleared restart data
	r.mu.Unlock()

	// Record stop operation
	if r.history != nil {
		r.history.Record("stop", "", "success", 0, nil, len(toStop))
	}
}
