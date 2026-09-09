package namespace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// persistState saves the current runtime state to disk. Must be called with r.mu held.
// Synchronous — small JSON struct, fast I/O, correct ordering guaranteed.
//
// It writes and reports, and does nothing else: it neither touches r.dirty nor
// logs. Both belong to persistUnderLock, which is what every production caller
// goes through — the dirty flag because settling it is the whole point of that
// helper, and the logging because a permanently failing store is retried on
// every runtime-loop iteration and a line per attempt would bury the daemon
// log (see notePersistOutcomeUnderLock).
func (r *Runtime) persistState() error {
	if r.statePersister == nil {
		return nil
	}
	state := &NsPersistedState{
		Status: r.status,
	}
	for name := range r.manualStoppedApps {
		state.ManualStoppedApps = append(state.ManualStoppedApps, name)
	}
	if len(r.editedAppPatches) > 0 {
		state.EditedAppPatches = make(map[string]json.RawMessage, len(r.editedAppPatches))
		maps.Copy(state.EditedAppPatches, r.editedAppPatches)
	}
	if len(r.editedFileEdits) > 0 {
		state.EditedFileEdits = make(map[string]FileEdit, len(r.editedFileEdits))
		maps.Copy(state.EditedFileEdits, r.editedFileEdits)
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
	if len(r.dependencyPins) > 0 {
		state.Dependencies = make(map[deps.ID]deps.DependencyState, len(r.dependencyPins))
		maps.Copy(state.Dependencies, r.dependencyPins)
	}
	if r.migrationJournal != nil {
		j := *r.migrationJournal
		state.DependencyMigration = &j
	}
	if r.lastMigration != nil {
		l := *r.lastMigration
		state.LastDependencyMigration = &l
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal namespace state: %w", err)
	}
	if err := r.statePersister.SaveNamespaceState(string(state.Status), string(data)); err != nil {
		return fmt.Errorf("persist namespace state: %w", err)
	}
	return nil
}

// persistUnderLock writes the whole record and settles the dirty flag
// honestly: a write that SUCCEEDED covers everything the runtime holds, so the
// loop tail has nothing left to do; a write that FAILED reached nothing, so
// the runtime still owes it and r.dirty stays set for the tail to retry — on
// every iteration, until one lands. persistState rebuilds the record from
// Runtime fields, so the retry needs nothing kept aside.
//
// Every production persist goes through here, the loop tail included. Two
// separate defects live in the alternative:
//
//   - An inline mutator that cleared r.dirty on a failed write lost the
//     mutation AND whatever unrelated change was pending with it. For a
//     dependency pin that is how 17 data reaches 18: after SetDependencyState
//     the in-memory pin already equals the running container's image, so
//     syncDependencyPinsUnderLock (pin writer #1) finds nothing to re-flag and
//     the pin survives in memory only, until the process restarts.
//   - The loop TAIL clearing r.dirty after its own failed write capped the
//     whole scheme at "retried once per marking": one failed retry dropped the
//     debt, so a store that was unavailable for two seconds lost the record as
//     thoroughly as one that was unavailable forever.
//
// The retry cannot deadlock or recurse: r.dirty is an atomic flag the
// runtimeLoop tail drains under its OWN r.mu.Lock, and persistState touches
// neither the flag nor the queue.
//
// Scope of the promise: the collector is the loop, so on a STOPPED namespace
// (where per-app config and mounted files are still editable) nothing retries
// until the next Start — whose first dirty tail then writes the CURRENT
// record, not a stale replay. That is still strictly better than dropping the
// debt, and it is what the log line says.
//
// mutation names the write for the log. Caller must hold r.mu.Lock.
func (r *Runtime) persistUnderLock(mutation string) error {
	err := r.persistState()
	r.notePersistOutcomeUnderLock(mutation, err)
	r.dirty.Store(err != nil)
	return err
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
// Callers (abnormal/unscheduled restarts only — these also bump the restart
// counter shown as the red "↻N" badge):
//   - T17a (liveness threshold) via handleLivenessProbeResult.
//   - T18 (crash / oom) via handleReconcileDiffResult.
//   - T33 (readopted_failing) via doStart adoption branch.
//
// A user-initiated RestartApp (incl. applying edited config to a running app) is
// deliberate, NOT abnormal: it emits no restart_event and does not bump the
// counter.
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
	// Leaving STARTING by any route (RUNNING, START_FAILED, STOPPING race…)
	// ends the init-container phase — clear the flag so the AppDto init
	// progress fields don't linger on a stale state. Entering STARTING is
	// untouched: beginStartingUnderLock sets initActive BEFORE calling here.
	if old == AppStatusStarting && s != AppStatusStarting {
		app.initActive = false
	}
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

// emitInitStepEventUnderLock buffers an `app_init_step` SSE event carrying the
// app's current init-container progress: Current = 1-based step index,
// Total = init container count, After = short step name (derived from the init
// image). All three are zero/empty once the init phase is over (T12) — the UI
// treats that as "clear the init suffix". Must be called with r.mu held
// (emitEvent appends to eventBuffer); callers emit only when the step index
// actually changed, so rapid init chains produce at most one event per step.
func (r *Runtime) emitInitStepEventUnderLock(app *AppRuntime) {
	step, total, name := appInitProgress(app)
	r.emitEvent(api.EventDto{
		Type:        "app_init_step",
		Timestamp:   time.Now().UnixMilli(),
		NamespaceID: r.nsID,
		AppName:     app.Name,
		After:       name,
		Current:     step,
		Total:       total,
	})
}
