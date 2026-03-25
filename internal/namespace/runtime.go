package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/niceteck/citeck-launcher/internal/api"
	"github.com/niceteck/citeck-launcher/internal/appdef"
	"github.com/niceteck/citeck-launcher/internal/docker"
)

// NsRuntimeStatus represents namespace lifecycle states.
type NsRuntimeStatus string

const (
	NsStatusStopped  NsRuntimeStatus = "STOPPED"
	NsStatusStarting NsRuntimeStatus = "STARTING"
	NsStatusRunning  NsRuntimeStatus = "RUNNING"
	NsStatusStopping NsRuntimeStatus = "STOPPING"
	NsStatusStalled  NsRuntimeStatus = "STALLED"
)

// AppRuntimeStatus represents per-app lifecycle states.
type AppRuntimeStatus string

const (
	AppStatusReadyToPull    AppRuntimeStatus = "READY_TO_PULL"
	AppStatusPulling        AppRuntimeStatus = "PULLING"
	AppStatusPullFailed     AppRuntimeStatus = "PULL_FAILED"
	AppStatusReadyToStart   AppRuntimeStatus = "READY_TO_START"
	AppStatusDepsWaiting    AppRuntimeStatus = "DEPS_WAITING"
	AppStatusStarting       AppRuntimeStatus = "STARTING"
	AppStatusRunning        AppRuntimeStatus = "RUNNING"
	AppStatusFailed         AppRuntimeStatus = "FAILED"
	AppStatusStartFailed    AppRuntimeStatus = "START_FAILED"
	AppStatusStoppingFailed AppRuntimeStatus = "STOPPING_FAILED"
	AppStatusStopped        AppRuntimeStatus = "STOPPED"
)

// AppRuntime holds the state for a single app.
type AppRuntime struct {
	Name        string
	Status      AppRuntimeStatus
	StatusText  string
	Def         appdef.ApplicationDef
	ContainerID string
	CPU         string
	Memory      string
}

// EventCallback is called when namespace or app state changes.
type EventCallback func(event api.EventDto)

// Runtime manages the full namespace lifecycle.
// All mutable state is protected by mu. setStatus/setAppStatus must only be called
// while mu is held by the caller.
type Runtime struct {
	mu          sync.RWMutex
	status      NsRuntimeStatus
	config      *NamespaceConfig
	apps        map[string]*AppRuntime
	docker      *docker.Client
	workspace   string
	nsID        string
	volumesBase string
	eventCb     EventCallback
	cmdCh       chan command
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

type commandType int

const (
	cmdStart commandType = iota
	cmdStop
	cmdRegenerate
)

type command struct {
	typ  commandType
	apps []appdef.ApplicationDef
}

func NewRuntime(cfg *NamespaceConfig, dockerClient *docker.Client, workspace, volumesBase string) *Runtime {
	return &Runtime{
		status:      NsStatusStopped,
		config:      cfg,
		apps:        make(map[string]*AppRuntime),
		docker:      dockerClient,
		workspace:   workspace,
		nsID:        cfg.ID,
		volumesBase: volumesBase,
		cmdCh:       make(chan command, 16),
	}
}

func (r *Runtime) SetEventCallback(cb EventCallback) {
	r.eventCb = cb
}

func (r *Runtime) Status() NsRuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

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

func (r *Runtime) ToNamespaceDto() api.NamespaceDto {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]api.AppDto, 0, len(r.apps))
	for _, app := range r.apps {
		apps = append(apps, api.AppDto{
			Name:   app.Name,
			Status: string(app.Status),
			Image:  app.Def.Image,
			CPU:    app.CPU,
			Memory: app.Memory,
		})
	}
	return api.NamespaceDto{
		ID:        r.nsID,
		Name:      r.config.Name,
		Status:    string(r.status),
		BundleRef: r.config.BundleRef.String(),
		Apps:      apps,
	}
}

func (r *Runtime) Start(apps []appdef.ApplicationDef) {
	r.wg.Add(1)
	go r.runLoop()
	r.cmdCh <- command{typ: cmdStart, apps: apps}
}

func (r *Runtime) Stop() {
	r.cmdCh <- command{typ: cmdStop}
}

func (r *Runtime) Shutdown() {
	select {
	case r.cmdCh <- command{typ: cmdStop}:
	default:
	}
	r.wg.Wait()
}

func (r *Runtime) Regenerate(apps []appdef.ApplicationDef) {
	r.cmdCh <- command{typ: cmdRegenerate, apps: apps}
}

// StopApp stops a single app by name. Returns error if not found or not running.
func (r *Runtime) StopApp(appName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	app, ok := r.apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	if app.ContainerID == "" {
		return fmt.Errorf("app %q has no container", appName)
	}

	ctx := context.Background()
	containerName := r.docker.ContainerName(appName)
	if err := r.docker.StopAndRemoveContainer(ctx, containerName); err != nil {
		return fmt.Errorf("stop app %q: %w", appName, err)
	}
	app.ContainerID = ""
	r.setAppStatus(app, AppStatusStopped)
	return nil
}

// RestartApp stops and re-starts a single app.
func (r *Runtime) RestartApp(appName string) error {
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}

	// Stop existing container
	ctx := context.Background()
	if app.ContainerID != "" {
		containerName := r.docker.ContainerName(appName)
		_ = r.docker.StopAndRemoveContainer(ctx, containerName)
		app.ContainerID = ""
	}
	r.setAppStatus(app, AppStatusStarting)
	r.mu.Unlock()

	// Re-start in background
	r.wg.Add(1)
	go r.restartApp(ctx, appName)
	return nil
}

func (r *Runtime) restartApp(ctx context.Context, appName string) {
	defer r.wg.Done()

	r.mu.RLock()
	app := r.apps[appName]
	appDef := app.Def
	r.mu.RUnlock()

	// Create and start container
	containerName := r.docker.ContainerName(appName)
	_ = r.docker.StopAndRemoveContainer(ctx, containerName)

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

// setStatus must be called with r.mu held.
func (r *Runtime) setStatus(s NsRuntimeStatus) {
	old := r.status
	r.status = s
	slog.Info("Namespace status changed", "from", old, "to", s)
	if r.eventCb != nil {
		r.eventCb(api.EventDto{
			Type: "namespace_status", Timestamp: time.Now().UnixMilli(),
			NamespaceID: r.nsID, Before: string(old), After: string(s),
		})
	}
}

// setAppStatus must be called with r.mu held.
func (r *Runtime) setAppStatus(app *AppRuntime, s AppRuntimeStatus) {
	old := app.Status
	app.Status = s
	slog.Info("App status changed", "app", app.Name, "from", old, "to", s)
	if r.eventCb != nil {
		r.eventCb(api.EventDto{
			Type: "app_status", Timestamp: time.Now().UnixMilli(),
			NamespaceID: r.nsID, AppName: app.Name, Before: string(old), After: string(s),
		})
	}
}

func (r *Runtime) runLoop() {
	defer r.wg.Done()
	slog.Info("Namespace runtime thread started", "namespace", r.nsID)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case cmd := <-r.cmdCh:
			switch cmd.typ {
			case cmdStart:
				r.doStart(cmd.apps)
			case cmdStop:
				r.doStop()
				return
			case cmdRegenerate:
				r.doStop()
				r.doStart(cmd.apps)
			}
		case <-ticker.C:
			r.mu.Lock()
			r.updateStats()
			r.checkStatus()
			r.mu.Unlock()
		}
	}
}

func (r *Runtime) doStart(apps []appdef.ApplicationDef) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	r.mu.Lock()
	r.setStatus(NsStatusStarting)

	// Create network (outside lock is fine — Docker call, no shared state)
	r.mu.Unlock()
	if _, err := r.docker.CreateNetwork(ctx); err != nil {
		slog.Error("Failed to create network", "err", err)
	}
	r.mu.Lock()

	// Initialize app runtimes
	for _, appDef := range apps {
		r.apps[appDef.Name] = &AppRuntime{
			Name: appDef.Name, Status: AppStatusReadyToPull, Def: appDef,
		}
	}

	// Launch each app in its own goroutine, tracked by wg
	for _, app := range r.apps {
		r.setAppStatus(app, AppStatusPulling)
		r.wg.Add(1)
		go r.pullAndStartApp(ctx, app.Name)
	}
	r.mu.Unlock()
}

func (r *Runtime) pullAndStartApp(ctx context.Context, appName string) {
	defer r.wg.Done()

	r.mu.RLock()
	app := r.apps[appName]
	appDef := app.Def
	r.mu.RUnlock()

	// Pull image if not present
	if appDef.Image != "" && !r.docker.ImageExists(ctx, appDef.Image) {
		slog.Info("Pulling image", "app", appName, "image", appDef.Image)
		if err := r.docker.PullImage(ctx, appDef.Image); err != nil {
			slog.Error("Pull failed", "app", appName, "err", err)
			r.mu.Lock()
			r.setAppStatus(app, AppStatusPullFailed)
			app.StatusText = err.Error()
			r.mu.Unlock()
			return
		}
	}

	// Wait for dependencies
	if !r.waitForDeps(ctx, appName) {
		return // context cancelled (shutdown)
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusStarting)
	r.mu.Unlock()

	// Run init containers
	r.runInitContainers(ctx, appName, appDef)

	// Create and start main container
	containerName := r.docker.ContainerName(appName)
	_ = r.docker.StopAndRemoveContainer(ctx, containerName)

	id, err := r.docker.CreateContainer(ctx, appDef, r.volumesBase)
	if err != nil {
		slog.Error("Create container failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	if err := r.docker.StartContainer(ctx, id); err != nil {
		slog.Error("Start container failed", "app", appName, "err", err)
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
			slog.Error("Startup probe failed", "app", appName, "err", err)
			r.mu.Lock()
			r.setAppStatus(app, AppStatusStartFailed)
			app.StatusText = err.Error()
			r.mu.Unlock()
			return
		}
	}

	// Run init actions (after startup probe — e.g. postgres DB creation)
	for _, action := range appDef.InitActions {
		if len(action.Exec) > 0 {
			slog.Info("Running init action", "app", appName, "cmd", action.Exec)
			_, exitCode, err := r.docker.ExecInContainer(ctx, id, action.Exec)
			if err != nil || exitCode != 0 {
				slog.Warn("Init action failed", "app", appName, "err", err, "exitCode", exitCode)
			}
		}
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusRunning)
	r.mu.Unlock()
}

func (r *Runtime) waitForDeps(ctx context.Context, appName string) bool {
	r.mu.RLock()
	app := r.apps[appName]
	deps := app.Def.DependsOn
	r.mu.RUnlock()

	if len(deps) == 0 {
		return true
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusDepsWaiting)
	r.mu.Unlock()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			r.mu.RLock()
			allReady := true
			for dep := range deps {
				if depApp, ok := r.apps[dep]; ok && depApp.Status != AppStatusRunning {
					allReady = false
					break
				}
			}
			r.mu.RUnlock()
			if allReady {
				return true
			}
		}
	}
}

func (r *Runtime) runInitContainers(ctx context.Context, appName string, appDef appdef.ApplicationDef) {
	for _, initC := range appDef.InitContainers {
		slog.Info("Running init container", "app", appName, "image", initC.Image)
		initDef := appdef.ApplicationDef{
			Name: appName + "-init", Image: initC.Image,
			Cmd: initC.Cmd, Volumes: initC.Volumes, Environments: initC.Environments,
		}
		if !r.docker.ImageExists(ctx, initC.Image) {
			if err := r.docker.PullImage(ctx, initC.Image); err != nil {
				slog.Warn("Init container pull failed", "app", appName, "err", err)
				continue
			}
		}
		initName := r.docker.ContainerName(appName + "-init")
		_ = r.docker.StopAndRemoveContainer(ctx, initName)
		initID, err := r.docker.CreateContainer(ctx, initDef, r.volumesBase)
		if err != nil {
			slog.Warn("Init container create failed", "app", appName, "err", err)
			continue
		}
		if err := r.docker.StartContainer(ctx, initID); err != nil {
			slog.Warn("Init container start failed", "app", appName, "err", err)
		}
		// Wait for init container to EXIT (not start)
		r.docker.WaitForContainerExit(ctx, initID, 60*time.Second)
		_ = r.docker.RemoveContainer(ctx, initID)
	}
}

func (r *Runtime) waitForStartup(ctx context.Context, appName, containerID string, conditions []appdef.StartupCondition) error {
	for _, cond := range conditions {
		if cond.Probe != nil {
			if err := r.waitForProbe(ctx, containerID, cond.Probe); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) waitForProbe(ctx context.Context, containerID string, probe *appdef.AppProbeDef) error {
	delay := probe.InitialDelaySeconds
	if delay <= 0 {
		delay = 5
	}
	period := probe.PeriodSeconds
	if period <= 0 {
		period = 10
	}
	threshold := probe.FailureThreshold
	if threshold <= 0 {
		threshold = 10000
	}

	time.Sleep(time.Duration(delay) * time.Second)

	shortID := truncateID(containerID)

	for attempt := 0; attempt < threshold; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if probe.Exec != nil {
			_, exitCode, err := r.docker.ExecInContainer(ctx, containerID, probe.Exec.Command)
			if err == nil && exitCode == 0 {
				slog.Info("Exec probe passed", "container", shortID, "attempt", attempt)
				return nil
			}
		}
		if probe.HTTP != nil {
			publishedPort := r.docker.GetPublishedPort(ctx, containerID, probe.HTTP.Port)
			if attempt == 0 || attempt%10 == 0 {
				slog.Info("HTTP probe", "container", shortID,
					"containerPort", probe.HTTP.Port, "publishedPort", publishedPort,
					"path", probe.HTTP.Path, "attempt", attempt)
			}
			if publishedPort > 0 && httpProbeCheck(publishedPort, probe.HTTP.Path, probe.TimeoutSeconds) {
				slog.Info("HTTP probe passed", "container", shortID, "port", publishedPort, "attempt", attempt)
				return nil
			}
		}
		time.Sleep(time.Duration(period) * time.Second)
	}

	return fmt.Errorf("probe failed after %d attempts", threshold)
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

	// Wait for all app goroutines to finish (they check ctx.Done)
	// Note: wg includes runLoop itself, so we can't Wait here.
	// Instead, give goroutines time to notice cancellation.
	time.Sleep(500 * time.Millisecond)

	r.mu.Lock()
	ctx := context.Background()
	for _, app := range r.apps {
		if app.ContainerID != "" {
			slog.Info("Stopping app", "app", app.Name)
			_ = r.docker.StopAndRemoveContainer(ctx, r.docker.ContainerName(app.Name))
			r.setAppStatus(app, AppStatusStopped)
		}
	}
	_ = r.docker.RemoveNetwork(ctx)
	r.apps = make(map[string]*AppRuntime)
	r.setStatus(NsStatusStopped)
	r.mu.Unlock()
}

func (r *Runtime) updateStats() {
	ctx := context.Background()
	for _, app := range r.apps {
		if app.ContainerID == "" || app.Status != AppStatusRunning {
			continue
		}
		stats, err := r.docker.ContainerStats(ctx, app.ContainerID)
		if err != nil {
			continue
		}
		app.CPU = fmt.Sprintf("%.1f%%", stats.CPUPercent)
		app.Memory = formatMemory(stats.MemUsage, stats.MemLimit)
	}
}

func (r *Runtime) checkStatus() {
	if r.status != NsStatusStarting && r.status != NsStatusRunning {
		return
	}
	allRunning := true
	anyFailed := false
	for _, app := range r.apps {
		if app.Status != AppStatusRunning {
			allRunning = false
		}
		if app.Status == AppStatusStartFailed || app.Status == AppStatusPullFailed {
			anyFailed = true
		}
	}
	if allRunning && r.status != NsStatusRunning {
		r.setStatus(NsStatusRunning)
	}
	if anyFailed && r.status == NsStatusStarting {
		r.setStatus(NsStatusStalled)
	}
}

func formatMemory(usage, limit int64) string {
	if limit <= 0 {
		return formatBytes(usage)
	}
	return fmt.Sprintf("%s / %s", formatBytes(usage), formatBytes(limit))
}

func httpProbeCheck(port int, path string, timeoutSec int) bool {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func formatBytes(b int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%dM", b/mb)
	default:
		return fmt.Sprintf("%dK", b/1024)
	}
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
