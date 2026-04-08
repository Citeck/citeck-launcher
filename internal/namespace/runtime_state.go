package namespace

import (
	"log/slog"
	"maps"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
)

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
		maps.Copy(state.EditedApps, r.editedApps)
	}
	for name := range r.editedLockedApps {
		state.EditedLockedApps = append(state.EditedLockedApps, name)
	}
	if r.cachedBundle != nil && !r.cachedBundle.IsEmpty() {
		state.CachedBundle = r.cachedBundle
	}
	if len(r.restartEvents) > 0 {
		state.RestartEvents = make([]RestartEvent, len(r.restartEvents))
		copy(state.RestartEvents, r.restartEvents)
	}
	if len(r.restartCounts) > 0 {
		state.RestartCounts = make(map[string]int, len(r.restartCounts))
		maps.Copy(state.RestartCounts, r.restartCounts)
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

// RestartEvent records a container restart with its cause and diagnostics.
type RestartEvent struct {
	Timestamp   string `json:"ts"`
	App         string `json:"app"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
	Diagnostics string `json:"diagnostics"`
}

const maxRestartEvents = 100

// resetRetry clears retry state for an app. Must be called with r.mu held.
func (r *Runtime) resetRetry(appName string) {
	if r.retryState != nil {
		delete(r.retryState, appName)
	}
}

// RestartEvents returns a copy of the restart event log.
func (r *Runtime) RestartEvents() []RestartEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]RestartEvent, len(r.restartEvents))
	copy(result, r.restartEvents)
	return result
}

// recordRestartEvent adds a restart event. Must be called with r.mu held.
func (r *Runtime) recordRestartEvent(evt RestartEvent) {
	r.restartEvents = append(r.restartEvents, evt)
	if len(r.restartEvents) > maxRestartEvents {
		r.restartEvents = r.restartEvents[len(r.restartEvents)-maxRestartEvents:]
	}
	r.emitEvent(api.EventDto{
		Type: "restart_event", Timestamp: time.Now().UnixMilli(),
		NamespaceID: r.nsID, AppName: evt.App, After: evt.Reason,
	})
}

// incrementRestartCount bumps the restart counter. Must be called with r.mu held.
func (r *Runtime) incrementRestartCount(appName string) {
	r.restartCounts[appName]++
	if app, ok := r.apps[appName]; ok {
		app.RestartCount = r.restartCounts[appName]
	}
}

// RestoreRestartState restores persisted restart events and counts (called before Start).
func (r *Runtime) RestoreRestartState(events []RestartEvent, counts map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(events) > 0 {
		r.restartEvents = make([]RestartEvent, len(events))
		copy(r.restartEvents, events)
	}
	if len(counts) > 0 {
		r.restartCounts = make(map[string]int, len(counts))
		maps.Copy(r.restartCounts, counts)
	}
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
	// Clear liveness failure counter when app leaves RUNNING state
	if old == AppStatusRunning && s != AppStatusRunning {
		delete(r.livenessFailures, app.Name)
	}
	// Wake all goroutines waiting for dependency status changes
	close(r.statusNotify)
	r.statusNotify = make(chan struct{})
}
