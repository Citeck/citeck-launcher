package namespace

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// NsRuntimeStatus represents namespace lifecycle states.
type NsRuntimeStatus string

// Namespace lifecycle states.
const (
	NsStatusStopped  NsRuntimeStatus = "STOPPED"
	NsStatusStarting NsRuntimeStatus = "STARTING"
	NsStatusRunning  NsRuntimeStatus = "RUNNING"
	NsStatusStopping NsRuntimeStatus = "STOPPING"
	NsStatusStalled  NsRuntimeStatus = "STALLED"
)

// AppRuntimeStatus represents per-app lifecycle states.
type AppRuntimeStatus string

// Per-app lifecycle states.
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
	Name         string
	Status       AppRuntimeStatus
	StatusText   string
	Def          appdef.ApplicationDef
	ContainerID  string
	CPU          string
	Memory       string
	RestartCount int
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
	config          *Config
	apps            map[string]*AppRuntime
	docker          docker.RuntimeClient
	actionSvc       *actions.Service
	ownsActions     bool // true if this runtime created its own action service
	running         atomic.Bool
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
	cachedBundle           *bundle.Def                      // last successfully resolved bundle (persisted)
	retryState             map[string]retryInfo             // retry tracking for failed apps
	livenessFailures       map[string]int                   // consecutive liveness probe failure counts
	restartCounts          map[string]int                   // total restart counts per app
	restartEvents          []RestartEvent                   // ring buffer of restart events
	statusNotify           chan struct{}                     // closed+recreated on every app status change
	cmdCh              chan command
	stopCh             chan struct{}       // dedicated stop signal that can't be dropped
	detachCh           chan struct{}       // exit runLoop without stopping containers (binary upgrade)
	pullSem            chan struct{}       // limits concurrent image pulls
	reconcilerCfg      *ReconcilerConfig  // optional override from daemon.yml
	defaultStopTimeout int                // from daemon.yml docker.stopTimeout; 0 = use hardcoded default (15s)
	shutdownOnce       sync.Once
	statsRunning       atomic.Bool        // guards against overlapping updateStats goroutines
	runCtx          context.Context    // set by doStart, canceled by doStop
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
	maps.Copy(result, r.manualStoppedApps)
	return result
}

// WaitForInitialReconcile blocks until the namespace leaves STARTING state
// (transitions to RUNNING, STALLED, or any other state). Returns immediately
// if the namespace is already past STARTING. Respects context cancellation.
func (r *Runtime) WaitForInitialReconcile(ctx context.Context) {
	for {
		r.mu.RLock()
		status := r.status
		notify := r.statusNotify
		r.mu.RUnlock()
		if status != NsStatusStarting {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-notify:
		}
	}
}

// SetCachedBundle updates the cached bundle definition (persisted for fallback on resolve failures).
func (r *Runtime) SetCachedBundle(def *bundle.Def) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if def != nil && !def.IsEmpty() {
		r.cachedBundle = def
	}
}

// SetManualStoppedApps restores persisted manual stopped apps (called before Start).
// Takes a defensive copy so the caller's map can't be mutated through runtime operations.
func (r *Runtime) SetManualStoppedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manualStoppedApps = maps.Clone(apps)
	if r.manualStoppedApps == nil {
		r.manualStoppedApps = make(map[string]bool)
	}
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
// Takes a defensive copy — the generator's map may be reused.
func (r *Runtime) SetDependsOnDetachedApps(apps map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependsOnDetachedApps = maps.Clone(apps)
}

type commandType int

const (
	cmdStart commandType = iota
	cmdRegenerate
)

type command struct {
	typ       commandType
	apps      []appdef.ApplicationDef
	cfg       *Config     // non-nil for cmdRegenerate when config changed (reload)
	bundleDef *bundle.Def // non-nil to update cached bundle (successful resolve)
}

// NewRuntime creates a new namespace runtime with a dedicated action service.
func NewRuntime(cfg *Config, dockerClient docker.RuntimeClient, volumesBase string) *Runtime {
	return NewRuntimeWithActions(cfg, dockerClient, volumesBase, nil)
}

// NewRuntimeWithActions creates a runtime with an externally provided action service.
// If actionSvc is nil, a new dedicated service is created.
func NewRuntimeWithActions(cfg *Config, dockerClient docker.RuntimeClient, volumesBase string, actionSvc *actions.Service) *Runtime {
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
		nsID:              cfg.ID,
		volumesBase:       volumesBase,
		manualStoppedApps: make(map[string]bool),
		editedApps:        make(map[string]appdef.ApplicationDef),
		editedLockedApps:  make(map[string]bool),
		livenessFailures:  make(map[string]int),
		restartCounts:     make(map[string]int),
		statusNotify:      make(chan struct{}),
		cmdCh:             make(chan command, 16),
		stopCh:            make(chan struct{}, 1),
		detachCh:          make(chan struct{}, 1),
		pullSem:           make(chan struct{}, 4),
		eventCh:           make(chan api.EventDto, 256),
	}
	go r.dispatchLoop()
	return r
}

// SetEventCallback registers a callback for namespace and app state change events.
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

// Status returns the current namespace lifecycle status.
func (r *Runtime) Status() NsRuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Apps returns a snapshot of all app states.
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
