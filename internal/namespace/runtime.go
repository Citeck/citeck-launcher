package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/niceteck/citeck-launcher/internal/appdef"
	"github.com/niceteck/citeck-launcher/internal/api"
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
	AppStatusReadyToPull   AppRuntimeStatus = "READY_TO_PULL"
	AppStatusPulling       AppRuntimeStatus = "PULLING"
	AppStatusPullFailed    AppRuntimeStatus = "PULL_FAILED"
	AppStatusReadyToStart  AppRuntimeStatus = "READY_TO_START"
	AppStatusDepsWaiting   AppRuntimeStatus = "DEPS_WAITING"
	AppStatusStarting      AppRuntimeStatus = "STARTING"
	AppStatusRunning       AppRuntimeStatus = "RUNNING"
	AppStatusFailed        AppRuntimeStatus = "FAILED"
	AppStatusStartFailed   AppRuntimeStatus = "START_FAILED"
	AppStatusStoppingFailed AppRuntimeStatus = "STOPPING_FAILED"
	AppStatusStopped       AppRuntimeStatus = "STOPPED"
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
	stopCh      chan struct{}
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
	apps []appdef.ApplicationDef // for regenerate
}

// NewRuntime creates a namespace runtime.
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
		stopCh:      make(chan struct{}),
	}
}

func (r *Runtime) SetEventCallback(cb EventCallback) {
	r.eventCb = cb
}

// Status returns the current namespace status.
func (r *Runtime) Status() NsRuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Apps returns a snapshot of all app runtimes.
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

// ToNamespaceDto converts runtime state to API DTO.
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

// Start begins the namespace runtime goroutine and starts apps.
func (r *Runtime) Start(apps []appdef.ApplicationDef) {
	r.wg.Add(1)
	go r.runLoop()
	r.cmdCh <- command{typ: cmdStart, apps: apps}
}

// Stop sends a stop command.
func (r *Runtime) Stop() {
	r.cmdCh <- command{typ: cmdStop}
}

// Shutdown stops the runtime goroutine.
func (r *Runtime) Shutdown() {
	close(r.stopCh)
	r.wg.Wait()
}

// Regenerate updates the namespace with new app definitions.
func (r *Runtime) Regenerate(apps []appdef.ApplicationDef) {
	r.cmdCh <- command{typ: cmdRegenerate, apps: apps}
}

func (r *Runtime) setStatus(s NsRuntimeStatus) {
	old := r.status
	r.status = s
	slog.Info("Namespace status changed", "from", old, "to", s)
	if r.eventCb != nil {
		r.eventCb(api.EventDto{
			Type:        "namespace_status",
			Timestamp:   time.Now().UnixMilli(),
			NamespaceID: r.nsID,
			Before:      string(old),
			After:       string(s),
		})
	}
}

func (r *Runtime) setAppStatus(app *AppRuntime, s AppRuntimeStatus) {
	old := app.Status
	app.Status = s
	slog.Info("App status changed", "app", app.Name, "from", old, "to", s)
	if r.eventCb != nil {
		r.eventCb(api.EventDto{
			Type:        "app_status",
			Timestamp:   time.Now().UnixMilli(),
			NamespaceID: r.nsID,
			AppName:     app.Name,
			Before:      string(old),
			After:       string(s),
		})
	}
}

func (r *Runtime) runLoop() {
	defer r.wg.Done()
	slog.Info("Namespace runtime thread started", "namespace", r.nsID)

	for {
		select {
		case <-r.stopCh:
			r.doStop()
			return
		case cmd := <-r.cmdCh:
			r.mu.Lock()
			switch cmd.typ {
			case cmdStart:
				r.doStart(cmd.apps)
			case cmdStop:
				r.doStop()
			case cmdRegenerate:
				r.doRegenerate(cmd.apps)
			}
			r.mu.Unlock()
		case <-time.After(5 * time.Second):
			// Periodic check: update stats and status
			r.mu.Lock()
			r.updateStats()
			r.checkStatus()
			r.mu.Unlock()
		}
	}
}

func (r *Runtime) doStart(apps []appdef.ApplicationDef) {
	r.setStatus(NsStatusStarting)
	ctx := context.Background()

	// Create network
	if _, err := r.docker.CreateNetwork(ctx); err != nil {
		slog.Error("Failed to create network", "err", err)
	}

	// Initialize app runtimes
	for _, appDef := range apps {
		ar := &AppRuntime{
			Name:   appDef.Name,
			Status: AppStatusReadyToPull,
			Def:    appDef,
		}
		r.apps[appDef.Name] = ar
	}

	// Start apps in dependency order
	go r.startApps(ctx)
}

func (r *Runtime) startApps(ctx context.Context) {
	for {
		r.mu.Lock()
		allDone := true
		anyFailed := false

		for _, app := range r.apps {
			switch app.Status {
			case AppStatusReadyToPull:
				allDone = false
				go r.pullAndStart(ctx, app.Name)
				r.setAppStatus(app, AppStatusPulling)
			case AppStatusPulling, AppStatusDepsWaiting, AppStatusStarting, AppStatusReadyToStart:
				allDone = false
			case AppStatusStartFailed, AppStatusPullFailed:
				anyFailed = true
			}
		}
		r.mu.Unlock()

		if allDone {
			r.mu.Lock()
			if anyFailed {
				r.setStatus(NsStatusStalled)
			} else {
				r.setStatus(NsStatusRunning)
			}
			r.mu.Unlock()
			return
		}

		time.Sleep(time.Second)
	}
}

func (r *Runtime) pullAndStart(ctx context.Context, appName string) {
	r.mu.RLock()
	app := r.apps[appName]
	appDef := app.Def
	r.mu.RUnlock()

	// Pull image
	if !r.docker.ImageExists(ctx, appDef.Image) {
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

	r.mu.Lock()
	// Check dependencies
	for dep := range appDef.DependsOn {
		depApp, exists := r.apps[dep]
		if exists && depApp.Status != AppStatusRunning {
			r.setAppStatus(app, AppStatusDepsWaiting)
			r.mu.Unlock()
			r.waitForDeps(ctx, appName)
			return
		}
	}
	r.setAppStatus(app, AppStatusStarting)
	r.mu.Unlock()

	r.startApp(ctx, appName)
}

func (r *Runtime) waitForDeps(ctx context.Context, appName string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
			r.mu.RLock()
			app := r.apps[appName]
			allReady := true
			for dep := range app.Def.DependsOn {
				depApp, exists := r.apps[dep]
				if exists && depApp.Status != AppStatusRunning {
					allReady = false
					break
				}
			}
			r.mu.RUnlock()

			if allReady {
				r.mu.Lock()
				r.setAppStatus(r.apps[appName], AppStatusStarting)
				r.mu.Unlock()
				r.startApp(ctx, appName)
				return
			}
		}
	}
}

func (r *Runtime) startApp(ctx context.Context, appName string) {
	r.mu.RLock()
	app := r.apps[appName]
	appDef := app.Def
	r.mu.RUnlock()

	slog.Info("Starting app", "app", appName, "image", appDef.Image)

	// Run init containers first
	for _, initC := range appDef.InitContainers {
		slog.Info("Running init container", "app", appName, "image", initC.Image)
		initDef := appdef.ApplicationDef{
			Name:    appName + "-init",
			Image:   initC.Image,
			Cmd:     initC.Cmd,
			Volumes: initC.Volumes,
			Environments: initC.Environments,
		}
		// Pull init image if needed
		if !r.docker.ImageExists(ctx, initC.Image) {
			if err := r.docker.PullImage(ctx, initC.Image); err != nil {
				slog.Warn("Init container pull failed", "app", appName, "image", initC.Image, "err", err)
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
		// Wait for init container to finish
		r.docker.WaitForContainer(ctx, initID, 60*time.Second)
		_ = r.docker.RemoveContainer(ctx, initID)
	}

	// Remove existing container if any
	containerName := r.docker.ContainerName(appName)
	_ = r.docker.StopAndRemoveContainer(ctx, containerName)

	// Create container
	id, err := r.docker.CreateContainer(ctx, appDef, r.volumesBase)
	if err != nil {
		slog.Error("Create container failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	// Start container
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

	// Wait for startup probe FIRST (container must be healthy before init actions)
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

	// Run init actions AFTER startup probe (e.g. postgres DB creation needs DB ready)
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

	for attempt := 0; attempt < threshold; attempt++ {
		if probe.Exec != nil {
			_, exitCode, err := r.docker.ExecInContainer(ctx, containerID, probe.Exec.Command)
			if err == nil && exitCode == 0 {
				slog.Info("Exec probe passed", "container", containerID[:12], "attempt", attempt)
				return nil
			}
		}
		if probe.HTTP != nil {
			publishedPort := r.docker.GetPublishedPort(ctx, containerID, probe.HTTP.Port)
			if attempt == 0 || attempt%10 == 0 {
				slog.Info("HTTP probe", "container", containerID[:12], "containerPort", probe.HTTP.Port, "publishedPort", publishedPort, "path", probe.HTTP.Path, "attempt", attempt)
			}
			if publishedPort > 0 {
				if httpProbeCheck(publishedPort, probe.HTTP.Path, probe.TimeoutSeconds) {
					slog.Info("HTTP probe passed", "container", containerID[:12], "port", publishedPort, "attempt", attempt)
					return nil
				}
			}
		}
		time.Sleep(time.Duration(period) * time.Second)
	}

	return fmt.Errorf("probe failed after %d attempts", threshold)
}

func (r *Runtime) doStop() {
	r.setStatus(NsStatusStopping)
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
}

func (r *Runtime) doRegenerate(apps []appdef.ApplicationDef) {
	// For now, stop and restart with new definitions
	r.doStop()
	r.doStart(apps)
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
		return fmt.Sprintf("%s", formatBytes(usage))
	}
	return fmt.Sprintf("%s / %s", formatBytes(usage), formatBytes(limit))
}

// httpProbeCheck does a HTTP GET to localhost:port/path and returns true if status 200.
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
