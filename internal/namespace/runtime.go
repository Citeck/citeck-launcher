package namespace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/nsactions"
	"github.com/docker/docker/pkg/stdcopy"
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
	AppStatusStopping       AppRuntimeStatus = "STOPPING"
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

// RegistryAuthFunc returns registry credentials for a given image, or nil if none.
type RegistryAuthFunc func(image string) *docker.RegistryAuth

// Runtime manages the full namespace lifecycle.
// All mutable state is protected by mu. setStatus/setAppStatus must only be called
// while mu is held by the caller.
type Runtime struct {
	mu              sync.RWMutex
	status          NsRuntimeStatus
	config          *NamespaceConfig
	apps            map[string]*AppRuntime
	docker          docker.RuntimeClient
	actionSvc       *actions.Service
	ownsActions     bool // true if this runtime created its own action service
	running         atomic.Bool
	workspace       string
	nsID            string
	volumesBase     string
	eventCb            atomic.Pointer[EventCallback]
	eventCh            chan api.EventDto
	registryAuthFn     RegistryAuthFunc
	history            *OperationHistory
	manualStoppedApps  map[string]bool
	editedApps         map[string]appdef.ApplicationDef // user-edited app defs
	editedLockedApps       map[string]bool                  // locked edits survive regeneration
	dependsOnDetachedApps  map[string]bool                  // detached apps that trigger regen on restart
	lastApps               []appdef.ApplicationDef          // last app defs passed to doStart
	retryState             map[string]retryInfo             // retry tracking for failed apps
	statusNotify           chan struct{}                     // closed+recreated on every app status change
	cmdCh              chan command
	stopCh             chan struct{}       // dedicated stop signal that can't be dropped
	pullSem            chan struct{}       // limits concurrent image pulls
	reconcilerCfg      *ReconcilerConfig  // optional override from daemon.yml
	defaultStopTimeout int                // from daemon.yml docker.stopTimeout; 0 = use hardcoded default (10s)
	shutdownOnce       sync.Once
	statsRunning       atomic.Bool        // guards against overlapping updateStats goroutines
	runCtx          context.Context    // set by doStart, cancelled by doStop
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	appWg           sync.WaitGroup     // tracks only app start/restart goroutines (not runLoop)
	reconcileWg     sync.WaitGroup     // tracks reconciler goroutines
}

// SetRegistryAuthFunc sets the function used to look up registry credentials for image pulls.
func (r *Runtime) SetRegistryAuthFunc(fn RegistryAuthFunc) {
	r.registryAuthFn = fn
}

// SetHistory sets the operation history logger.
func (r *Runtime) SetHistory(h *OperationHistory) {
	r.history = h
}

// SetReconcilerConfig overrides default reconciler settings (from daemon.yml).
func (r *Runtime) SetReconcilerConfig(cfg ReconcilerConfig) {
	r.reconcilerCfg = &cfg
}

// SetPullConcurrency overrides the pull semaphore capacity (from daemon.yml).
func (r *Runtime) SetPullConcurrency(n int) {
	if n > 0 {
		r.pullSem = make(chan struct{}, n)
	}
}

// SetDefaultStopTimeout overrides the default stop timeout (from daemon.yml docker.stopTimeout).
func (r *Runtime) SetDefaultStopTimeout(seconds int) {
	if seconds > 0 {
		r.defaultStopTimeout = seconds
	}
}

// ManualStoppedApps returns a copy of manually stopped apps (for generator detached apps).
func (r *Runtime) ManualStoppedApps() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]bool, len(r.manualStoppedApps))
	for k, v := range r.manualStoppedApps {
		result[k] = v
	}
	return result
}

// SetManualStoppedApps restores persisted manual stopped apps (called before Start).
func (r *Runtime) SetManualStoppedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manualStoppedApps = apps
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
	r.persistState()
}

// SetDependsOnDetachedApps stores which detached apps trigger regeneration when restarted.
func (r *Runtime) SetDependsOnDetachedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependsOnDetachedApps = apps
}

// persistState saves the current runtime state to disk. Must be called with r.mu held.
// Synchronous — small JSON struct, fast I/O, correct ordering guaranteed.
func (r *Runtime) persistState() {
	if r.volumesBase == "" {
		return
	}
	state := &NsPersistedState{
		Status: r.status,
	}
	for name := range r.manualStoppedApps {
		state.ManualStoppedApps = append(state.ManualStoppedApps, name)
	}
	if len(r.editedApps) > 0 {
		state.EditedApps = make(map[string]appdef.ApplicationDef, len(r.editedApps))
		for k, v := range r.editedApps {
			state.EditedApps[k] = v
		}
	}
	for name := range r.editedLockedApps {
		state.EditedLockedApps = append(state.EditedLockedApps, name)
	}
	if err := SaveNsState(r.volumesBase, r.nsID, state); err != nil {
		slog.Warn("Failed to persist namespace state", "err", err)
	}
}

type retryInfo struct {
	count       int
	lastAttempt time.Time
}

// retryCount returns the retry count for an app. Must be called with r.mu held.
func (r *Runtime) retryCount(appName string) int {
	if r.retryState == nil {
		return 0
	}
	return r.retryState[appName].count
}

// retryLastAttempt returns the last retry attempt time. Must be called with r.mu held.
func (r *Runtime) retryLastAttempt(appName string) time.Time {
	if r.retryState == nil {
		return time.Time{}
	}
	return r.retryState[appName].lastAttempt
}

// recordRetryAttempt increments retry count and records time. Must be called with r.mu held.
func (r *Runtime) recordRetryAttempt(appName string) {
	if r.retryState == nil {
		r.retryState = make(map[string]retryInfo)
	}
	info := r.retryState[appName]
	info.count++
	info.lastAttempt = time.Now()
	r.retryState[appName] = info
}

// resetRetry clears retry state for an app. Must be called with r.mu held.
func (r *Runtime) resetRetry(appName string) {
	if r.retryState != nil {
		delete(r.retryState, appName)
	}
}

type commandType int

const (
	cmdStart commandType = iota
	cmdRegenerate
)

type command struct {
	typ  commandType
	apps []appdef.ApplicationDef
}

// NewRuntime creates a new namespace runtime with a dedicated action service.
func NewRuntime(cfg *NamespaceConfig, dockerClient docker.RuntimeClient, workspace, volumesBase string) *Runtime {
	return NewRuntimeWithActions(cfg, dockerClient, workspace, volumesBase, nil)
}

// NewRuntimeWithActions creates a runtime with an externally provided action service.
// If actionSvc is nil, a new dedicated service is created.
func NewRuntimeWithActions(cfg *NamespaceConfig, dockerClient docker.RuntimeClient, workspace, volumesBase string, actionSvc *actions.Service) *Runtime {
	ownsActions := false
	if actionSvc == nil {
		actionSvc = actions.NewService(actions.ServiceConfig{})
		ownsActions = true
	}
	r := &Runtime{
		status:            NsStatusStopped,
		config:            cfg,
		apps:              make(map[string]*AppRuntime),
		docker:            dockerClient,
		actionSvc:         actionSvc,
		ownsActions:       ownsActions,
		workspace:         workspace,
		nsID:              cfg.ID,
		volumesBase:       volumesBase,
		manualStoppedApps: make(map[string]bool),
		editedApps:        make(map[string]appdef.ApplicationDef),
		editedLockedApps:  make(map[string]bool),
		statusNotify:      make(chan struct{}),
		cmdCh:             make(chan command, 16),
		stopCh:            make(chan struct{}, 1),
		pullSem:           make(chan struct{}, 4),
		eventCh:           make(chan api.EventDto, 256),
	}
	go r.dispatchLoop()
	return r
}

func (r *Runtime) SetEventCallback(cb EventCallback) {
	r.eventCb.Store(&cb)
}

// emitEvent pushes an event to the dispatch channel (non-blocking).
// Must be called with r.mu held. The event is delivered outside the lock by dispatchLoop.
func (r *Runtime) emitEvent(evt api.EventDto) {
	select {
	case r.eventCh <- evt:
	default:
	}
}

// dispatchLoop drains eventCh and calls eventCb outside any lock.
func (r *Runtime) dispatchLoop() {
	for evt := range r.eventCh {
		if cb := r.eventCb.Load(); cb != nil {
			(*cb)(evt)
		}
	}
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

func (r *Runtime) ToNamespaceDto() api.NamespaceDto {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]api.AppDto, 0, len(r.apps))
	for _, app := range r.apps {
		_, edited := r.editedApps[app.Name]
		apps = append(apps, api.AppDto{
			Name:       app.Name,
			Status:     string(app.Status),
			StatusText: app.StatusText,
			Image:      app.Def.Image,
			CPU:        app.CPU,
			Memory:     app.Memory,
			Kind:       KindToString(app.Def.Kind),
			Ports:      app.Def.Ports,
			Edited:     edited,
			Locked:     r.editedLockedApps[app.Name],
		})
	}
	return api.NamespaceDto{
		ID:        r.nsID,
		Name:      r.config.Name,
		Status:    string(r.status),
		BundleRef: r.config.BundleRef.String(),
		Apps:      apps,
		Links:     r.generateLinks(),
	}
}

// KindToString converts an ApplicationKind to its API string representation.
func KindToString(k appdef.ApplicationKind) string {
	switch k {
	case appdef.KindCiteckCore:
		return "CITECK_CORE"
	case appdef.KindCiteckCoreExtension:
		return "CITECK_CORE_EXTENSION"
	case appdef.KindCiteckAdditional:
		return "CITECK_ADDITIONAL"
	case appdef.KindThirdParty:
		return "THIRD_PARTY"
	default:
		return "UNKNOWN"
	}
}

// generateLinks builds quick links. Must be called with r.mu held.
func (r *Runtime) generateLinks() []api.LinkDto {
	if r.config == nil {
		return nil
	}
	proxyBase := r.proxyBaseURL()
	proxyHost := r.config.Proxy.Host
	if proxyHost == "" {
		proxyHost = "localhost"
	}

	links := []api.LinkDto{
		{Name: "ECOS UI", URL: proxyBase, Icon: "ecos", Order: -100},
		{Name: "Spring Boot Admin", URL: proxyBase + "/gateway/eapps/admin/wallboard", Icon: "spring", Order: -1},
		{Name: "RabbitMQ", URL: fmt.Sprintf("http://%s:15672", proxyHost), Icon: "rabbitmq", Order: 2},
		{Name: "MailHog", URL: fmt.Sprintf("http://%s:8025", proxyHost), Icon: "mailhog", Order: 1},
	}

	// Keycloak link (only if auth is KEYCLOAK)
	if r.config.Authentication.Type == AuthKeycloak {
		links = append(links, api.LinkDto{
			Name: "Keycloak Admin", URL: proxyBase + "/ecos-idp/auth/", Icon: "keycloak", Order: -10,
		})
	}

	// PgAdmin link (if app exists)
	if _, ok := r.apps["pgadmin"]; ok {
		links = append(links, api.LinkDto{
			Name: "PG Admin", URL: fmt.Sprintf("http://%s:5050", proxyHost), Icon: "postgres", Order: 0,
		})
	}

	// Global links (always available)
	links = append(links,
		api.LinkDto{Name: "Documentation", URL: "https://citeck-ecos.readthedocs.io/", Icon: "docs", Order: 100},
		api.LinkDto{Name: "AI Documentation Bot", URL: "https://t.me/haski_citeck_bot", Icon: "telegram", Order: 101},
	)

	return links
}

func (r *Runtime) proxyBaseURL() string {
	return BuildProxyBaseURL(r.config.Proxy)
}

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

func (r *Runtime) Stop() {
	select {
	case r.stopCh <- struct{}{}:
	default:
		// Already signaled
	}
}

func (r *Runtime) Shutdown() {
	r.shutdownOnce.Do(func() {
		if r.running.Load() {
			r.Stop()
			r.wg.Wait()
		}
		// Wait for all app/reconciler goroutines to finish before closing eventCh.
		// doStop already waits, but this is a belt-and-suspenders guard in case
		// Shutdown is called after running was already cleared.
		r.appWg.Wait()
		r.reconcileWg.Wait()
		close(r.eventCh) // stops dispatchLoop
		if r.ownsActions {
			r.actionSvc.Shutdown()
		}
	})
}

func (r *Runtime) Regenerate(apps []appdef.ApplicationDef) {
	select {
	case r.cmdCh <- command{typ: cmdRegenerate, apps: apps}:
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

// StopApp stops a single app by name. Returns error if not found or not running.
func (r *Runtime) StopApp(appName string) error {
	r.mu.Lock()
	app, ok := r.apps[appName]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("app %q not found", appName)
	}
	if app.ContainerID == "" {
		r.mu.Unlock()
		return fmt.Errorf("app %q has no container", appName)
	}
	containerName := r.docker.ContainerName(appName)
	r.mu.Unlock()

	// Blocking Docker call outside the lock — 2 minute timeout to prevent indefinite hang
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := r.docker.StopAndRemoveContainer(ctx, containerName, 0); err != nil {
		return fmt.Errorf("stop app %q: %w", appName, err)
	}

	r.mu.Lock()
	// Re-lookup app after releasing lock — the map may have changed during the Docker call
	if app, ok = r.apps[appName]; ok {
		app.ContainerID = ""
		r.setAppStatus(app, AppStatusStopped)
	}
	r.manualStoppedApps[appName] = true
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
	// cancelled on shutdown (would abort the stop mid-way).
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
		stopTimeout = 10
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

// setStatus must be called with r.mu held.
func (r *Runtime) setStatus(s NsRuntimeStatus) {
	old := r.status
	r.status = s
	slog.Info("Namespace status changed", "from", old, "to", s)
	r.emitEvent(api.EventDto{
		Type: "namespace_status", Timestamp: time.Now().UnixMilli(),
		NamespaceID: r.nsID, Before: string(old), After: string(s),
	})
	// Persist state on status change
	r.persistState()
}

// setAppStatus must be called with r.mu held.
func (r *Runtime) setAppStatus(app *AppRuntime, s AppRuntimeStatus) {
	old := app.Status
	app.Status = s
	slog.Info("App status changed", "app", app.Name, "from", old, "to", s)
	r.emitEvent(api.EventDto{
		Type: "app_status", Timestamp: time.Now().UnixMilli(),
		NamespaceID: r.nsID, AppName: app.Name, Before: string(old), After: string(s),
	})
	// Wake all goroutines waiting for dependency status changes
	close(r.statusNotify)
	r.statusNotify = make(chan struct{})
}

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

func (r *Runtime) doStart(apps []appdef.ApplicationDef) {
	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.runCtx = ctx
	r.cancel = cancel
	r.lastApps = apps
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
	for k, v := range r.editedLockedApps {
		editedLocked[k] = v
	}
	for k, v := range r.editedApps {
		editedApps[k] = v
	}
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
				r.docker.StopAndRemoveContainer(ctx, cn, 0)
			}(containerName)
		}
	}
	removeWg.Wait()

	// Verify reused containers are actually running (fast Docker inspect)
	for i, p := range plans {
		if p.reuse {
			inspCtx, inspCancel := context.WithTimeout(ctx, 5*time.Second)
			info, err := r.docker.InspectContainer(inspCtx, p.containerID)
			inspCancel()
			if err != nil || info.State == nil || info.State.Status != "running" {
				slog.Warn("Reused container not running, will recreate", "app", p.def.Name)
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

func (r *Runtime) pullAndStartApp(ctx context.Context, appName string) {
	defer r.appWg.Done()

	r.mu.RLock()
	app := r.apps[appName]
	if app == nil {
		r.mu.RUnlock()
		return
	}
	appDef := app.Def
	r.mu.RUnlock()

	// Pull image via action service — pull policy:
	// THIRD_PARTY: never re-pull (stable images)
	// Other apps: only re-pull if image tag contains "snapshot" (case-insensitive)
	if appDef.Image != "" {
		// Pull image under semaphore (max concurrent pulls)
		pullErr := func() error {
			select {
			case r.pullSem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-r.pullSem }()

			pullAlways := shouldPullImage(appDef.Kind, appDef.Image)
			var auth *docker.RegistryAuth
			if r.registryAuthFn != nil {
				auth = r.registryAuthFn(appDef.Image)
			}
			var lastProgressReport time.Time
			progressFn := func(currentMB, totalMB float64, pct int) {
				now := time.Now()
				if now.Sub(lastProgressReport) < time.Second {
					return
				}
				lastProgressReport = now
				r.mu.Lock()
				app.StatusText = fmt.Sprintf("Pulling: %.0fmb %d%%", totalMB, pct)
				r.mu.Unlock()
			}
			pullHandle := r.actionSvc.Execute(actions.ActionParams{
				Executor: &nsactions.PullExecutor{Docker: r.docker, PullAlways: pullAlways},
				Data:     &nsactions.PullData{AppName: appName, Image: appDef.Image, Auth: auth, ProgressFn: progressFn},
			})
			return pullHandle.Wait(ctx)
		}()

		if pullErr != nil {
			if ctx.Err() != nil {
				return // cancelled by shutdown — not a failure
			}
			r.mu.Lock()
			r.setAppStatus(app, AppStatusPullFailed)
			app.StatusText = pullErr.Error()
			r.mu.Unlock()
			return
		}

		// Clear pull status text and fetch image digest for deployment hash
		r.mu.Lock()
		app.StatusText = ""
		r.mu.Unlock()
		if digest := r.docker.GetImageDigest(ctx, appDef.Image); digest != "" {
			r.mu.Lock()
			appDef.ImageDigest = digest
			app.Def = appDef
			r.mu.Unlock()
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
	if err := r.runInitContainers(ctx, appName, appDef); err != nil {
		slog.Error("Init container failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = fmt.Sprintf("init container: %v", err)
		r.mu.Unlock()
		return
	}

	// Create and start container via action service
	startData := &nsactions.StartData{
		AppName: appName, AppDef: appDef, VolumesBase: r.volumesBase,
	}
	startHandle := r.actionSvc.Execute(actions.ActionParams{
		Executor: &nsactions.StartExecutor{Docker: r.docker},
		Data:     startData,
	})
	if err := startHandle.Wait(ctx); err != nil {
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	app.ContainerID = startData.ContainerID
	r.mu.Unlock()

	// Wait for startup probe
	if len(appDef.StartupConditions) > 0 {
		if err := r.waitForStartup(ctx, appName, startData.ContainerID, appDef.StartupConditions); err != nil {
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
			_, exitCode, err := r.docker.ExecInContainer(ctx, startData.ContainerID, action.Exec)
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

	for {
		r.mu.RLock()
		allReady := true
		for dep := range deps {
			depApp, ok := r.apps[dep]
			if !ok || depApp.Status != AppStatusRunning {
				allReady = false
				break
			}
		}
		// Capture current notify channel under the same lock to avoid races
		notify := r.statusNotify
		r.mu.RUnlock()

		if allReady {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-notify:
			// Status changed, re-check deps
		}
	}
}

func (r *Runtime) runInitContainers(ctx context.Context, appName string, appDef appdef.ApplicationDef) error {
	for _, initC := range appDef.InitContainers {
		slog.Info("Running init container", "app", appName, "image", initC.Image)
		initDef := appdef.ApplicationDef{
			Name: appName + "-init", Image: initC.Image,
			Cmd: initC.Cmd, Volumes: initC.Volumes, Environments: initC.Environments,
			Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "100m"}},
			IsInit:    true, // no restart policy for init containers
		}

		// Pull init image via action service
		var initAuth *docker.RegistryAuth
		if r.registryAuthFn != nil {
			initAuth = r.registryAuthFn(initC.Image)
		}
		pullHandle := r.actionSvc.Execute(actions.ActionParams{
			Executor: &nsactions.PullExecutor{Docker: r.docker, RetryDelays: nsactions.InitPullRetryDelays},
			Data:     &nsactions.PullData{AppName: appName, Image: initC.Image, Auth: initAuth},
		})
		if err := pullHandle.Wait(ctx); err != nil {
			return fmt.Errorf("pull init image %s: %w", initC.Image, err)
		}

		initName := r.docker.ContainerName(appName + "-init")
		_ = r.docker.StopAndRemoveContainer(ctx, initName, 0)
		initID, err := r.docker.CreateContainer(ctx, initDef, r.volumesBase)
		if err != nil {
			return fmt.Errorf("create init container for %s: %w", appName, err)
		}
		if err := r.docker.StartContainer(ctx, initID); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.docker.RemoveContainer(cleanupCtx, initID)
			cleanupCancel()
			return fmt.Errorf("start init container for %s: %w", appName, err)
		}
		// Wait for init container to EXIT (not start)
		if err := r.docker.WaitForContainerExit(ctx, initID, 60*time.Second); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.docker.RemoveContainer(cleanupCtx, initID)
			cleanupCancel()
			return fmt.Errorf("init container exited with error for %s: %w", appName, err)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.docker.RemoveContainer(cleanupCtx, initID)
		cleanupCancel()
	}
	return nil
}

func (r *Runtime) waitForStartup(ctx context.Context, appName, containerID string, conditions []appdef.StartupCondition) error {
	for _, cond := range conditions {
		if cond.Log != nil {
			if err := r.waitForLogPattern(ctx, containerID, cond.Log); err != nil {
				return err
			}
		}
		if cond.Probe != nil {
			if err := r.waitForProbe(ctx, containerID, cond.Probe); err != nil {
				return err
			}
		}
	}
	return nil
}

// waitForLogPattern watches Docker container logs for a regex pattern match using follow streaming.
func (r *Runtime) waitForLogPattern(ctx context.Context, containerID string, cond *appdef.LogStartupCondition) error {
	timeout := time.Duration(cond.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	pattern, err := regexp.Compile(cond.Pattern)
	if err != nil {
		return fmt.Errorf("invalid log pattern %q: %w", cond.Pattern, err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shortID := truncateID(containerID)

	// Use Docker follow to stream logs, demux through stdcopy to strip Docker multiplex headers
	rawReader, err := r.docker.ContainerLogsFollow(timeoutCtx, containerID, 50)
	if err != nil {
		return fmt.Errorf("follow logs %s: %w", shortID, err)
	}
	defer rawReader.Close()

	// Pipe demuxed output for clean line scanning
	pr, pw := io.Pipe()
	defer pr.Close() // unblocks stdcopy goroutine on early return
	go func() {
		stdcopy.StdCopy(pw, pw, rawReader)
		pw.Close()
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if pattern.MatchString(line) {
			slog.Debug("Log pattern matched", "container", shortID, "pattern", cond.Pattern)
			return nil
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("log pattern %q not found in %s after %v", cond.Pattern, shortID, timeout)
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
		threshold = 360 // ~1 hour with default 10s period
	}

	// Context-aware initial delay
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(delay) * time.Second):
	}

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
			if publishedPort > 0 && httpProbeCheck(ctx, publishedPort, probe.HTTP.Path, probe.TimeoutSeconds) {
				slog.Info("HTTP probe passed", "container", shortID, "port", publishedPort, "attempt", attempt)
				return nil
			}
		}
		// Context-aware period sleep
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(period) * time.Second):
		}
	}

	return fmt.Errorf("probe failed after %d attempts", threshold)
}

// shouldPullImage determines if an image should be re-pulled based on app kind and image tag.
// THIRD_PARTY apps: never re-pull. Others: only re-pull if tag contains "snapshot".
func shouldPullImage(kind appdef.ApplicationKind, img string) bool {
	if kind == appdef.KindThirdParty {
		return false
	}
	lower := strings.ToLower(img)
	return strings.Contains(lower, "snapshot")
}

type existingContainer struct {
	containerID string
	hash        string
	running     bool
}

// buildExistingContainerMap gets current Docker containers and their deployment hashes.
func (r *Runtime) buildExistingContainerMap(ctx context.Context) map[string]existingContainer {
	containers, err := r.docker.GetContainers(ctx)
	if err != nil {
		return nil
	}
	result := make(map[string]existingContainer)
	for _, c := range containers {
		appName := c.Labels[docker.LabelAppName]
		if appName == "" {
			continue
		}
		result[appName] = existingContainer{
			containerID: c.ID,
			hash:        c.Labels[docker.LabelAppHash],
			running:     c.State == "running",
		}
	}
	return result
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
	r.setStatus(NsStatusStopped)
	r.mu.Unlock()

	// Record stop operation
	if r.history != nil {
		r.history.Record("stop", "", "success", 0, nil, len(toStop))
	}
}

// updateStats fetches container stats in parallel (outside lock), then updates app state briefly under lock.
func (r *Runtime) updateStats() {
	// Snapshot running apps under read lock
	r.mu.RLock()
	type appRef struct {
		name        string
		containerID string
	}
	var targets []appRef
	for _, app := range r.apps {
		if app.ContainerID != "" && app.Status == AppStatusRunning {
			targets = append(targets, appRef{name: app.Name, containerID: app.ContainerID})
		}
	}
	r.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	// Fetch stats in parallel
	type statResult struct {
		name   string
		cpu    string
		memory string
	}
	results := make([]statResult, len(targets))
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, t := range targets {
		wg.Add(1)
		go func(idx int, ref appRef) {
			defer wg.Done()
			stats, err := r.docker.ContainerStats(ctx, ref.containerID)
			if err != nil {
				return
			}
			results[idx] = statResult{
				name:   ref.name,
				cpu:    fmt.Sprintf("%.1f%%", stats.CPUPercent),
				memory: formatMemory(stats.MemUsage, stats.MemLimit),
			}
		}(i, t)
	}
	wg.Wait()

	// Apply under write lock (brief)
	r.mu.Lock()
	for _, res := range results {
		if res.name == "" {
			continue
		}
		if app, ok := r.apps[res.name]; ok {
			app.CPU = res.cpu
			app.Memory = res.memory
		}
	}
	r.mu.Unlock()
}

func (r *Runtime) checkStatus() {
	if r.status != NsStatusStarting && r.status != NsStatusRunning && r.status != NsStatusStalled {
		return
	}
	allRunning := true
	anyFailed := false
	for _, app := range r.apps {
		// Skip manually-stopped apps — they are intentionally detached
		if r.manualStoppedApps[app.Name] {
			continue
		}
		if app.Status != AppStatusRunning {
			allRunning = false
		}
		if app.Status == AppStatusStartFailed || app.Status == AppStatusPullFailed {
			anyFailed = true
		}
	}
	if len(r.apps) > 0 && allRunning && r.status != NsStatusRunning {
		r.setStatus(NsStatusRunning)
	}
	if anyFailed && (r.status == NsStatusStarting || r.status == NsStatusRunning) {
		r.setStatus(NsStatusStalled)
	}
	// Recover from STALLED when failed apps have recovered
	if !anyFailed && r.status == NsStatusStalled {
		r.setStatus(NsStatusStarting)
	}
}

func formatMemory(usage, limit int64) string {
	if limit <= 0 {
		return formatBytes(usage)
	}
	return fmt.Sprintf("%s / %s", formatBytes(usage), formatBytes(limit))
}

// probeClient is a shared HTTP client for health probes.
// Reuses connections across probe invocations. Timeouts are set per-request via context.
var probeClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	},
}

func httpProbeCheck(ctx context.Context, port int, path string, timeoutSec int) bool {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
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
