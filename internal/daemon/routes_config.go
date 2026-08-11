package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// uiPrefThemeKey / uiPrefLocaleKey are the launcher-state keys under which the
// web UI's theme and locale are persisted server-side (survives a desktop
// webview localStorage wipe). The locale here takes precedence over the
// daemon.yml locale once the user has changed it in the UI.
const (
	uiPrefThemeKey  = "ui.theme"
	uiPrefLocaleKey = "ui.locale"
)

func (d *Daemon) handleDaemonStatus(w http.ResponseWriter, r *http.Request) {
	locale := d.daemonCfg.Locale
	var theme string
	if d.store != nil {
		if v, err := d.store.GetStateValue(uiPrefLocaleKey); err == nil && v != "" {
			locale = v
		}
		if v, err := d.store.GetStateValue(uiPrefThemeKey); err == nil {
			theme = v
		}
	}
	writeJSON(w, api.DaemonStatusDto{
		Running:    true,
		PID:        int64(os.Getpid()),
		Uptime:     time.Since(d.startTime).Milliseconds(),
		Version:    d.version,
		Workspace:  d.activeWorkspaceID(),
		SocketPath: d.socketPath,
		Desktop:    config.IsDesktopMode(),
		Locale:     locale,
		Theme:      theme,
	})
}

// uiPrefLocales is the set of locales the UI ships (i18n parity). PUT /ui-prefs
// rejects anything else so junk can't be persisted into launcher state.
var uiPrefLocales = map[string]bool{
	"en": true, "ru": true, "zh": true, "es": true,
	"de": true, "fr": true, "pt": true, "ja": true,
}

// handlePutUIPrefs persists the web UI theme/locale server-side. Empty fields
// are left unchanged. Theme must be "dark"|"light"; locale must be a shipped
// locale. See UIPrefsDto.
func (d *Daemon) handlePutUIPrefs(w http.ResponseWriter, r *http.Request) {
	var req api.UIPrefsDto
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "invalid request body")
		return
	}
	if d.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no storage backend")
		return
	}
	if req.Theme != "" {
		if req.Theme != "dark" && req.Theme != "light" {
			writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "theme must be \"dark\" or \"light\"")
			return
		}
		if err := d.store.SetStateValue(uiPrefThemeKey, req.Theme); err != nil {
			writeInternalError(w, fmt.Errorf("save theme: %w", err))
			return
		}
	}
	if req.Locale != "" {
		if !uiPrefLocales[req.Locale] {
			writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, "unknown locale")
			return
		}
		if err := d.store.SetStateValue(uiPrefLocaleKey, req.Locale); err != nil {
			writeInternalError(w, fmt.Errorf("save locale: %w", err))
			return
		}
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "ui prefs saved"})
}

func (d *Daemon) handleDaemonShutdown(w http.ResponseWriter, r *http.Request) {
	// leave_running=true keeps the platform containers alive (used for binary
	// upgrades). Strict parse — any unrecognized value is rejected so callers
	// don't silently fall through to a full shutdown when they meant detach.
	leaveRunning := false
	if raw := r.URL.Query().Get("leave_running"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest,
				"invalid leave_running value (must be true or false)")
			return
		}
		leaveRunning = v
	}
	msg := "Shutting down"
	if leaveRunning {
		msg = "Detaching daemon (containers will keep running)"
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: msg})
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.shutdown(leaveRunning)
	}()
}

func (d *Daemon) handleGetNamespace(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	runtime := act.runtime
	bundleErr := act.bundleError
	appDefs := act.appDefs
	if runtime == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	dto := runtime.ToNamespaceDto()
	// Owned by the daemon, not the runtime: it covers precisely the stretch
	// BEFORE the runtime is handed the command (see Daemon.updateInFlight), and
	// only for the namespace the pass is pinned to.
	dto.Updating = d.updateInFlight.Load() && d.updatingAppliesTo(act.nsConfig)
	dto.UpdateError, dto.UpdateErrorAt = d.updateFailureFor(act.nsConfig)
	// Name comes from the active config, which doReload updates SYNCHRONOUSLY on
	// edit; the runtime's own copy (ToNamespaceDto's r.config.Name) is refreshed
	// only by the ASYNC cmdRegenerate, so a name-only edit — which changes no app
	// state and emits no events — would otherwise leave the header showing the
	// previous name until something else happened to re-resolve the runtime.
	if act.nsConfig != nil {
		dto.Name = act.nsConfig.Name
	}
	if bundleErr != "" {
		dto.BundleError = bundleErr
	}
	// When namespace is stopped, runtime clears the app list. Populate from
	// the resolved config so the UI always shows the full service catalog.
	if len(dto.Apps) == 0 && len(appDefs) > 0 {
		dto.Apps = appDefsToStoppedApps(appDefs, runtime)
	}
	writeJSON(w, dto)
}

// appDefsToStoppedApps converts resolved app definitions into AppDto entries
// with STOPPED status. Used to populate the UI when namespace is not running.
// The Edited/Locked flags reflect any stored per-app config override so the
// editor's Reset button stays visible on a stopped namespace.
func appDefsToStoppedApps(defs []appdef.ApplicationDef, runtime *namespace.Runtime) []api.AppDto {
	apps := make([]api.AppDto, 0, len(defs))
	for _, def := range defs {
		if def.IsInit {
			continue // skip init containers
		}
		edited := runtime != nil && runtime.AppPatch(def.Name) != nil
		apps = append(apps, api.AppDto{
			Name:             def.Name,
			Status:           api.AppStatusStopped,
			Image:            def.Image,
			Kind:             namespace.KindToString(def.Kind),
			Ports:            def.Ports,
			Edited:           edited,
			Locked:           edited,
			EditedFilesCount: runtime.EditedFilesCountForApp(def.Name),
		})
	}
	return apps
}

func (d *Daemon) handleStartNamespace(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	runtime, appDefs := act.runtime, act.appDefs
	if runtime == nil || appDefs == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	// Both "Update And Start" (primary) and "Force Update And Start" (RMB menu)
	// pull the workspace / bundle repos before starting, so a stopped namespace
	// picks up new bundle versions instead of starting a stale set. The only
	// difference is timing: non-force respects the per-repo PullPeriod throttle
	// (skips the pull if the last sync is recent), force bypasses it and pulls
	// immediately — the force flag affects git only, not image pulling. Both
	// variants route through updateAndStartAsync, which always passes
	// refreshImages=true to doReloadEx: that's what makes Update & Start (either
	// variant) pre-pull :snapshot digests before the hash diff, unlike a
	// config-edit reload or boot auto-start (refreshImages=false, cached
	// digests only). Runs off the request goroutine (slow git I/O).
	force := r.URL.Query().Get("force") == "true"
	// Raise the "update in flight" flag SYNCHRONOUSLY, before the async pass is
	// even spawned and before this response is written, so the UI can never
	// observe an accepted click with no visible effect. Everything from here to
	// the runtime handoff (reloadMu wait, git pull, bundle resolve, generate)
	// leaves namespace and app statuses untouched, so this flag is the only
	// feedback available during it. Ordering matters: setting it after
	// updateAndStartAsync could race a pass that finishes and clears the flag
	// first, leaving it stuck on with nobody left to lower it.
	//
	// Pin the namespace the click was aimed at first, so the flag is raised
	// against it: the pass may sit on reloadMu for a while, and SwitchWorkspace /
	// namespace activation can replace the active namespace underneath it — the
	// pass must neither act on a namespace the user never clicked nor show
	// "Updating…" on one.
	nsID := ""
	if act.nsConfig != nil {
		nsID = act.nsConfig.ID
	}
	// A fresh click supersedes whatever the previous pass reported, so the UI
	// never shows a stale failure next to a running update.
	d.updateLastFailure.Store(nil)
	d.setUpdateInFlight(true, nsID)
	d.updateAndStartAsync(force, nsID)
	msg := "Namespace start requested"
	if force {
		msg = "Force update and start requested"
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: msg})
}

// updateFailure is why the last Update & Start pass did not happen, pinned to
// the namespace it targeted. `at` (epoch ms) lets a client show each failure
// exactly once instead of re-raising it on every refetch or remount.
type updateFailure struct {
	nsID string
	msg  string
	at   int64
}

// recordUpdateFailure stores a failure/refusal for the namespace the pass was
// aimed at. Callers should do this BEFORE returning, so the lowering of
// updateInFlight (and its broadcast) is what prompts the client to refetch and
// find it.
func (d *Daemon) recordUpdateFailure(nsID, msg string) {
	d.updateLastFailure.Store(&updateFailure{nsID: nsID, msg: msg, at: time.Now().UnixMilli()})
}

// updateFailureFor returns the recorded failure if it belongs to this namespace.
// Scoped like updatingAppliesTo: a failure from a pass aimed elsewhere must not
// pop up on an unrelated namespace.
func (d *Daemon) updateFailureFor(nsCfg *namespace.Config) (msg string, at int64) {
	f := d.updateLastFailure.Load()
	if f == nil {
		return "", 0
	}
	if f.nsID != "" && nsCfg != nil && f.nsID != nsCfg.ID {
		return "", 0
	}
	return f.msg, f.at
}

// updatingAppliesTo reports whether the in-flight update belongs to the given
// namespace. An absent or empty pin means "applies to whatever is active".
func (d *Daemon) updatingAppliesTo(nsCfg *namespace.Config) bool {
	p := d.updateInFlightNsID.Load()
	if p == nil || *p == "" || nsCfg == nil {
		return true
	}
	return *p == nsCfg.ID
}

// updateStartAction is what an Update & Start pass should do with the runtime,
// decided from the namespace's LIVE status after the reloadMu wait.
type updateStartAction int

const (
	updateStartRegenerate updateStartAction = iota // loop alive — recreate changed apps in place
	updateStartStart                               // loop down — hand it a fresh Start
	updateStartSkip                                // mid-teardown — neither verb can land
)

// updateStartActionFor maps a namespace status onto that choice. Getting it
// wrong is silent — Runtime.Start early-returns on its `running` CompareAndSwap
// ("Runtime already running, ignoring Start()") and a cmdRegenerate posted to a
// dead loop is never drained — so a mis-mapped status throws the whole pass
// (git pull + resolve + generate) away after HTTP already answered 200.
//
// STARTING belongs with RUNNING, and that is the non-obvious one: the queue this
// action uses deliberately creates the case (a click during an in-flight pass
// queues a fresh one), and a real bundle sits in STARTING for minutes, so it is
// the status a second click is most likely to sample.
//
// STOPPING gets neither verb. Start looks plausible because it calls
// awaitStoppedLoopExit first, but that only waits once `shutdownComplete` is
// CLOSED, and signalShutdown fires at the very tail of the stop chain (after
// RemoveNetwork) — i.e. only when the status is already STOPPED. Through the
// whole real STOPPING window (up to longStopTimeout, 60s for Java apps) the
// channel is open, the wait returns immediately, `running` is still true and the
// CAS drops the command. Regenerate is equally wrong: it would fight the
// teardown. So the pass bails out loudly BEFORE paying for a git pull whose
// result nothing would consume. The click is not honored — the user has just
// asked for a stop — but it is refused in the log rather than silently absorbed.
func updateStartActionFor(st namespace.NsRuntimeStatus) updateStartAction {
	switch st {
	case namespace.NsStatusStopped:
		return updateStartStart
	case namespace.NsStatusStopping:
		return updateStartSkip
	case namespace.NsStatusRunning, namespace.NsStatusStalled, namespace.NsStatusStarting:
		return updateStartRegenerate
	default:
		slog.Warn("Update and start: unhandled namespace status, treating as running", "status", st)
		return updateStartRegenerate
	}
}

// setUpdateInFlight flips the Updating flag and tells every connected client at
// once. The SSE event is what makes the feedback immediate: the web store
// refetches on any event, so the indicator appears without waiting for the 3s
// poll — and because the flag lives in the DTO too, a client that connects or
// reloads mid-update still sees it.
func (d *Daemon) setUpdateInFlight(v bool, nsID string) {
	if v {
		d.updateInFlightNsID.Store(&nsID)
	} else {
		d.updateInFlightNsID.Store(nil)
	}
	if d.updateInFlight.Swap(v) == v {
		return // no transition; don't spam subscribers
	}
	after := "false"
	if v {
		after = "true"
	}
	d.broadcastEvent(api.EventDto{
		Type:      "namespace_updating",
		Timestamp: time.Now().UnixMilli(),
		After:     after,
	})
}

// updateAndStartAsync runs "Update And Start" / "Force Update And Start" off the
// request goroutine: a git pull (throttled by PullPeriod unless forceGitPull
// bypasses it) re-resolves the bundle, then a running namespace recreates changed
// apps while a stopped one is started with the fresh set. doReloadEx holds
// reloadMu and does slow I/O, so it must not block the HTTP handler.
//
// This is the sole caller that passes refreshImages=true to doReloadEx — Update
// & Start is the one action that should pay the :snapshot pre-pull digest
// refresh cost (it's the explicit "give me the latest" gesture).
//
// It therefore WAITS for reloadMu instead of TryLock-and-dropping like the other
// async reload paths. Dropping was wrong here: the in-flight reload it folded
// into is nearly always a config-edit or boot reload with refreshImages=false,
// so the digest refresh never happened and the user saw a click that did
// nothing — while HTTP had already answered 200 "requested". At most one pass
// waits (updatePending); extra clicks fold into that pass, which starts after
// they arrive and so satisfies them.
//
// nsID pins the namespace the click targeted; an empty string disables the check.
func (d *Daemon) updateAndStartAsync(forceGitPull bool, nsID string) {
	if forceGitPull {
		d.updatePendingForce.Store(true)
	}
	// Record the target BEFORE the CAS, for exactly the reason the force flag is
	// recorded there: a click that folds still has to hand its intent to the pass
	// that will run on its behalf.
	d.updatePendingNsID.Store(&nsID)
	if !d.updatePending.CompareAndSwap(false, true) {
		slog.Info("Update-and-start folded into an already-queued update")
		return
	}
	go func() {
		d.reloadMu.Lock()
		defer d.reloadMu.Unlock()
		// Lower it on the way out — but only if no further pass is queued behind
		// us, otherwise the indicator would blink off between two chained
		// updates. Registered AFTER the Unlock defer so it runs BEFORE it (LIFO).
		// This check is a best-effort narrowing, not a guarantee: the re-assert
		// above is what actually closes the race it cannot see.
		defer func() {
			if !d.updatePending.Load() {
				d.setUpdateInFlight(false, "")
			}
		}()
		// Release the queue slot now that the lock is ours, so a click landing
		// during our own run queues a fresh pass rather than being dropped.
		//
		// Order matters, and it is the opposite of what it looks like: clear
		// updatePending FIRST, then consume the force flag. These are two
		// separate atomics, so a click can land between them; only this order
		// leaves no interleaving that strands a force.
		//   - click before the Store: its CAS fails and it folds, but our Swap
		//     below picks its force up and we run forced — and our doReloadEx
		//     runs after the click, so it is honored, not swallowed.
		//   - click between Store and Swap: its CAS succeeds (a fresh pass is
		//     queued) AND our Swap still picks the force up, so it is honored
		//     immediately; the queued pass just repeats throttled. Harmless.
		//   - click after the Swap: its CAS succeeds and the pass it queues
		//     consumes the force itself.
		// The reverse order (Swap then Store) has a hole: a click in that gap
		// both fails its CAS *and* misses our Swap, so updatePendingForce stays
		// set with no pass left to consume it — leaking an unthrottled git pull
		// into the next, unrelated plain click.
		d.updatePending.Store(false)
		force := d.updatePendingForce.Swap(false) || forceGitPull
		// Same consume-after-clear ordering as the force flag, and for the same
		// reason: a click landing in the gap either has its target picked up here
		// or queues a pass carrying its own.
		target := nsID
		if p := d.updatePendingNsID.Swap(nil); p != nil {
			target = *p
		}
		// Re-assert the in-flight flag, now that we know which namespace this
		// pass is actually for. The handler raised it, but a PREVIOUS pass may
		// have lowered it in the gap between its own updatePending check and our
		// CAS: the raise and the CAS both run on the request goroutine, which
		// never takes reloadMu, so holding the lock orders nothing against them.
		// Re-raising is self-healing and idempotent (setUpdateInFlight broadcasts
		// only on a real transition). Position inside the critical section is
		// irrelevant to that race — the previous pass lowers while holding the
		// lock, so it has always finished by the time we get here.
		d.setUpdateInFlight(true, target)

		// Re-sample the status here, not on the request goroutine: waiting for
		// reloadMu can outlast the sample (the in-flight reload may itself have
		// started or stopped the namespace), and startNotRegenerate must match
		// the runtime's actual state or Runtime.Start early-returns
		// ("Runtime already running") and the command is silently lost.
		act := d.active()
		if act.runtime == nil {
			slog.Warn("Update and start skipped: no active namespace runtime")
			d.recordUpdateFailure(target, "no active namespace runtime")
			return
		}
		if target != "" && act.nsConfig != nil && act.nsConfig.ID != target {
			slog.Info("Update and start skipped: active namespace changed while queued",
				"clicked", target, "active", act.nsConfig.ID)
			return
		}
		action := updateStartActionFor(act.runtime.Status())
		if action == updateStartSkip {
			slog.Warn("Update and start skipped: namespace is stopping — retry once it has stopped")
			d.recordUpdateFailure(target, "namespace is stopping — retry once it has stopped")
			return
		}
		if err := d.invokeReloadEx(force, action == updateStartStart, true); err != nil {
			slog.Warn("Update and start failed", "err", err)
			d.recordUpdateFailure(target, err.Error())
		}
	}()
}

func (d *Daemon) handleStopNamespace(w http.ResponseWriter, r *http.Request) {
	runtime := d.active().runtime
	if runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	runtime.Stop()
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace stop requested"})
}

func (d *Daemon) handleReloadNamespace(w http.ResponseWriter, r *http.Request) {
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	if act := d.active(); act.runtime == nil || act.nsConfig == nil || act.bundleDef == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}

	if err := d.doReload(); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Reload requested"})
}

func (d *Daemon) handleUpgradeNamespace(w http.ResponseWriter, r *http.Request) {
	var req api.UpgradeRequestDto
	if err := readJSON(r, &req); err != nil || req.BundleRef == "" {
		writeError(w, http.StatusBadRequest, "bundleRef required")
		return
	}
	ref, err := bundle.ParseRef(req.BundleRef)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bundleRef: %v", err))
		return
	}

	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	act := d.active()
	if act.runtime == nil || act.nsConfig == nil || act.bundleDef == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	nsID := act.nsConfig.ID
	currentRef := act.nsConfig.BundleRef

	if ref == currentRef {
		writeJSON(w, api.ActionResultDto{Success: true, Message: "already on " + req.BundleRef})
		return
	}

	// Update the stored config with the new bundleRef (via the choke-point).
	nsCfg, err := d.loadNamespaceConfigFromStore(act.workspaceID, nsID)
	if err != nil {
		writeInternalError(w, fmt.Errorf("load config: %w", err))
		return
	}
	nsCfg.BundleRef = ref
	data, err := namespace.MarshalNamespaceConfig(nsCfg)
	if err != nil {
		writeInternalError(w, fmt.Errorf("marshal config: %w", err))
		return
	}
	if err := d.persistNamespaceConfig(act.workspaceID, nsID, data); err != nil {
		writeInternalError(w, fmt.Errorf("write config: %w", err))
		return
	}

	slog.Info("Bundle upgrade requested", "from", currentRef, "to", ref)

	// Trigger reload with the updated config
	if err := d.doReload(); err != nil {
		writeInternalError(w, fmt.Errorf("reload after upgrade: %w", err))
		return
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("upgraded from %s to %s", currentRef, ref),
	})
}

func (d *Daemon) handleGetAppliedConfig(w http.ResponseWriter, _ *http.Request) {
	rt := d.active().runtime
	if rt == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime not started")
		return
	}
	cfg := rt.AppliedConfig()
	if cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "no applied config")
		return
	}
	data, err := namespace.MarshalNamespaceConfig(cfg)
	if err != nil {
		writeInternalError(w, fmt.Errorf("marshal applied config: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	_, _ = w.Write(data)
}

func (d *Daemon) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	wsID, nsID := d.activeNsKey()
	raw, ok, err := d.store.LoadNamespaceConfig(wsID, nsID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "config not found")
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	_, _ = w.Write([]byte(raw))
}

func (d *Daemon) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	wsID, nsID := d.activeNsKey()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Validate + persist the user's exact bytes through the choke-point.
	if err := d.persistNamespaceConfig(wsID, nsID, body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, fmt.Sprintf("invalid config: %s", err.Error()))
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "Configuration saved"})
}

func (d *Daemon) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable write deadline for long-lived SSE stream
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// EventSource automatically attaches Last-Event-ID on browser-driven
	// reconnects. The ?lastSeq= query param is the explicit override used by
	// the longop watchdog path (and by tests) so the client controls replay
	// regardless of EventSource quirks.
	lastSeq := parseLastEventID(r)

	ch, replayCutoff, ok2 := d.addSubscriber()
	if !ok2 {
		writeError(w, http.StatusServiceUnavailable, "too many SSE subscribers")
		return
	}
	defer d.removeSubscriber(ch)

	// replayCutoff is captured under the same eventMu that broadcastEvent
	// uses for fanout, so the partition is total: events with Seq > cutoff
	// are guaranteed to arrive live on `ch`; events with Seq <= cutoff were
	// broadcast before the subscription and reach this client only via the
	// replay loop below.

	if lastSeq > 0 && d.eventRing != nil {
		replay, ringOK := d.eventRing.since(lastSeq)
		if !ringOK {
			// Buffer wrapped past the gap — tell the client to resync. The
			// store's existing gap-detection (event.seq > lastSeq + 1) will
			// fire fetchData() once live events resume.
			fmt.Fprint(w, "event: resync\ndata: {}\n\n")
			flusher.Flush()
		}
		wrote := false
		for _, evt := range replay {
			if evt.Seq > replayCutoff {
				continue
			}
			writeSSEEvent(w, evt)
			wrote = true
		}
		if wrote || !ringOK {
			flusher.Flush()
		}
	}

	ctx := r.Context()
	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			writeSSEEvent(w, evt)
			flusher.Flush()
			ticker.Reset(sseKeepaliveInterval)
		case <-ticker.C:
			// A NAMED `ping` event (not a bare `: comment`) so the client can
			// OBSERVE it: the desktop client uses ping arrival to tell a live
			// SSE stream from one the Windows WebView2 asset-server silently
			// buffers (which delivers no incremental frames) and falls back to
			// polling when pings stop. EventSource routes this to a `ping`
			// listener, never to onmessage, so it carries no seq and is inert
			// to gap detection.
			fmt.Fprint(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

// sseKeepaliveInterval is how often handleEvents emits a `ping` on an otherwise
// idle stream. Kept below the client's SSE-staleness threshold so a healthy
// stream never trips the desktop polling fallback (see web store SSE_STALE_MS).
const sseKeepaliveInterval = 10 * time.Second

// parseLastEventID resolves the client's last-seen Seq from either the
// standard SSE Last-Event-ID header (browser EventSource auto-reconnect) or
// an explicit ?lastSeq= query param (frontend watchdog-driven reconnect).
// Returns 0 on absence or malformed input — treated as a fresh subscription.
func parseLastEventID(r *http.Request) int64 {
	if q := r.URL.Query().Get("lastSeq"); q != "" {
		if n, err := strconv.ParseInt(q, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		if n, err := strconv.ParseInt(h, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func writeSSEEvent(w io.Writer, evt api.EventDto) {
	data, _ := json.Marshal(evt)
	// Emit `id:` so browser EventSource captures it for Last-Event-ID on
	// reconnect. Field order (id before data) matches the SSE spec example.
	fmt.Fprintf(w, "id: %d\ndata: %s\n\n", evt.Seq, data)
}

func (d *Daemon) handleDaemonLogs(w http.ResponseWriter, r *http.Request) {
	logPath := config.DaemonLogPath()

	tail := parseTailParam(r, 200, 10000)
	follow := r.URL.Query().Get("follow") == "true"

	// Read at most last 2MB of the file to avoid OOM on large logs
	const maxReadSize = 2 * 1024 * 1024
	f, err := os.Open(logPath) //nolint:gosec // path is from config.DaemonLogPath(), not user input
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("daemon log not found: %s", logPath))
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	readSize := stat.Size()
	if readSize > maxReadSize {
		if _, seekErr := f.Seek(-maxReadSize, io.SeekEnd); seekErr != nil {
			writeInternalError(w, seekErr)
			return
		}
		readSize = maxReadSize
	}
	data, err := io.ReadAll(io.LimitReader(f, readSize))
	if err != nil {
		writeInternalError(w, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	// Disable write deadline before any write in follow mode — the initial tail
	// can be up to 2MB and may exceed the server's 30s WriteTimeout on slow connections.
	if follow {
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Time{})
	}

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))

	if !follow {
		return
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Track file position for incremental reads
	offset := stat.Size()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			f2, err := os.Open(logPath) //nolint:gosec // G304: logPath is derived from internal config
			if err != nil {
				return
			}
			st, err := f2.Stat()
			if err != nil {
				_ = f2.Close()
				return
			}
			newSize := st.Size()
			if newSize <= offset {
				// File was rotated or truncated — reset
				if newSize < offset {
					offset = 0
				}
				_ = f2.Close()
				continue
			}
			if _, seekErr := f2.Seek(offset, io.SeekStart); seekErr != nil {
				_ = f2.Close()
				return
			}
			chunk, readErr := io.ReadAll(io.LimitReader(f2, newSize-offset))
			_ = f2.Close()
			if readErr != nil || len(chunk) == 0 {
				continue
			}
			offset = newSize
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func (d *Daemon) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	if d.logLevel == nil {
		writeError(w, http.StatusServiceUnavailable, "log level control not available")
		return
	}
	var req struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var level slog.Level
	switch strings.ToLower(req.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown level %q (debug, info, warn, error)", req.Level))
		return
	}
	d.logLevel.Set(level)
	slog.Info("Log level changed", "level", level.String())
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("log level set to %s", level.String())})
}
