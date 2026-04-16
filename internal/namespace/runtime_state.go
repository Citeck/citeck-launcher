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
//
// Most state mutators set r.dirty.Store(true); runtimeLoop invokes persistState
// once per iteration via the dirty-flag tail. Direct callers (StopApp /
// StartApp / UpdateAppDef / SetAppLocked / doDetach / RestartApp) persist
// inline because they record durable user intent that must survive a crash
// between loop iterations. After an inline persist, the caller clears
// r.dirty.Store(false) so the loop tail does not redundantly re-persist.
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
	// Use r.nowFunc() (not time.Now()) so fake-clock tests can deterministically
	// drive retry timing in lockstep with retryDueFor's clock source.
	info.lastAttempt = r.nowFunc()
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

// retryDueFor reports whether the exponential backoff window has elapsed for
// appName at the given now. The first retry (count=1) waits 1 minute; each
// subsequent failure doubles up to a 10-minute cap (count=2 → 2m, count=3 →
// 4m, count=4 → 8m, count≥5 → 10m). Apps with no recorded failure (zero
// lastAttempt) are treated as due immediately. Must be called with r.mu held
// (read-only access to retryState).
//
// Used by T24 (PULL_FAILED → READY_TO_PULL) and T25 (START_FAILED → READY_TO_START).
// Backoff parity with reconciler.go:170-184 (kept for cross-version behavior).
func (r *Runtime) retryDueFor(appName string, now time.Time) bool {
	count := r.retryCount(appName)
	last := r.retryLastAttempt(appName)
	if last.IsZero() {
		return true
	}
	// Floor: count<1 (e.g. lastAttempt set without a recordRetryAttempt bump)
	// still yields the documented 1-minute first-retry backoff.
	if count < 1 {
		count = 1
	}
	// Defensive clamp before the shift: a perpetually-failing app could in
	// principle accumulate count beyond the bit width of an int Duration.
	// The 10-minute cap below makes anything ≥ ~log2(10) irrelevant, but we
	// clamp anyway so the shift can never overflow.
	if count > 30 {
		count = 30
	}
	backoff := min(time.Duration(1<<(count-1))*time.Minute, 10*time.Minute)
	return now.Sub(last) >= backoff
}

// RestartEvents returns a copy of the restart event log.
func (r *Runtime) RestartEvents() []RestartEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]RestartEvent, len(r.restartEvents))
	copy(result, r.restartEvents)
	return result
}

// emitRestartEvent is the SOLE write path for restart_event. It appends to
// r.restartEvents (with trim to maxRestartEvents) and buffers an SSE event.
// Callers:
//   - T17a (liveness threshold) via handleLivenessProbeResult.
//   - T18 (crash / oom) via handleReconcileDiffResult.
//   - T33 (readopted_failing) via doStart adoption branch.
//   - RestartApp (user_restart).
//
// Must be called with r.mu held. detail / diagnostics may be empty — they
// live on the persisted RestartEvent only, not in the EventDto payload.
func (r *Runtime) emitRestartEvent(app *AppRuntime, reason, detail, diagnostics string) {
	evt := RestartEvent{
		Timestamp:   r.nowFunc().UTC().Format(time.RFC3339),
		App:         app.Name,
		Reason:      reason,
		Detail:      detail,
		Diagnostics: diagnostics,
	}
	r.restartEvents = append(r.restartEvents, evt)
	if len(r.restartEvents) > maxRestartEvents {
		r.restartEvents = r.restartEvents[len(r.restartEvents)-maxRestartEvents:]
	}
	r.emitEvent(api.EventDto{
		Type: "restart_event", Timestamp: r.nowFunc().UnixMilli(),
		NamespaceID: r.nsID, AppName: app.Name, After: reason,
	})
}

// lastRestartReason returns the reason of the most recent RestartEvent whose
// App matches name, or "" if none found. Reverse-scans the runtime-scoped ring
// buffer. Self-mutes T33: after readopted_failing is appended for app X, the
// next call returns "readopted_failing" (not in the bad-set) → no duplicate
// WARN. Must be called with r.mu held (read-only access).
func (r *Runtime) lastRestartReason(name string) string {
	for i := len(r.restartEvents) - 1; i >= 0; i-- {
		if r.restartEvents[i].App == name {
			return r.restartEvents[i].Reason
		}
	}
	return ""
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
	if old == s {
		return
	}
	r.status = s
	slog.Info("Namespace status changed", "from", old, "to", s)
	r.emitEvent(api.EventDto{
		Type: "namespace_status", Timestamp: time.Now().UnixMilli(),
		NamespaceID: r.nsID, Before: string(old), After: string(s),
	})
	// Fan out to nsStatusListeners under the existing Lock. Non-blocking send
	// — a full subscriber buffer drops the event; the subscriber must re-poll
	// r.Status() on its own timeout.
	for _, ch := range r.nsStatusListeners {
		select {
		case ch <- s:
		default:
			// Slow subscriber; drop. Subscriber re-polls r.Status() on timeout.
		}
	}
	// Flag dirty instead of fsyncing under Lock. The runtimeLoop tail coalesces
	// multiple transitions in one iteration into a single persistState call.
	// Writers to dirty must do so *under* Lock (same Lock as the state mutation)
	// so a subsequent reader in the loop sees dirty=true together with the new
	// state.
	r.dirty.Store(true)
}

// setAppStatus must be called with r.mu held. Mutates app.Status, buffers an
// app_status SSE event, and flushes signalCh so runtimeLoop wakes within
// ≤100ms to run updateNsStatus + flushEvents.
//
// Per-app Status itself is NOT persisted (only namespace-level r.status is).
// Callers that ALSO mutate persistable fields (restartCounts, restartEvents,
// manualStoppedApps, editedApps) must set r.dirty themselves — this function
// only flips events, not the dirty flag.
func (r *Runtime) setAppStatus(app *AppRuntime, s AppRuntimeStatus) {
	old := app.Status
	if old == s {
		return
	}
	app.Status = s
	slog.Info("App status changed", "app", app.Name, "from", old, "to", s)
	r.emitEvent(api.EventDto{
		Type: "app_status", Timestamp: time.Now().UnixMilli(),
		NamespaceID: r.nsID, AppName: app.Name, Before: string(old), After: string(s),
	})
	// Clear liveness failure counter and stale stats when app leaves RUNNING state.
	// Also clear the per-app liveness schedule so a future RUNNING transition
	// starts with a fresh InitialDelaySeconds offset rather than firing
	// immediately.
	if old == AppStatusRunning && s != AppStatusRunning {
		delete(r.livenessFailures, app.Name)
		delete(r.livenessNextAt, app.Name)
		app.CPU = ""
		app.Memory = ""
	}
	// When an app newly enters RUNNING, seed the liveness schedule with an
	// InitialDelaySeconds offset so tick() dispatches probes at the right
	// cadence. Apps without a LivenessProbe definition are not seeded.
	if s == AppStatusRunning && old != AppStatusRunning && app.Def.LivenessProbe != nil {
		r.livenessNextAt[app.Name] = r.nowFunc().Add(initialDelayForProbe(app.Def.LivenessProbe))
	}
}
