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
	// immediately. Kotlin 1.x parity: forceUpdate flips ONLY the git policy to
	// REQUIRED — image pulling stays normal (a present release tag is reused;
	// only :snapshot tags refresh), so force never re-pulls release tags. Runs
	// off the request goroutine (slow git I/O).
	force := r.URL.Query().Get("force") == "true"
	st := runtime.Status()
	wasRunning := st == namespace.NsStatusRunning || st == namespace.NsStatusStalled
	d.updateAndStartAsync(force, wasRunning)
	msg := "Namespace start requested"
	if force {
		msg = "Force update and start requested"
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: msg})
}

// updateAndStartAsync runs "Update And Start" / "Force Update And Start" off the
// request goroutine: a git pull (throttled by PullPeriod unless forceGitPull
// bypasses it) re-resolves the bundle, then a running namespace recreates changed
// apps while a stopped one is started with the fresh set. doReloadEx holds
// reloadMu and does slow I/O, so it must not block the HTTP handler; a concurrent
// reload already in progress (TryLock fails) is treated as satisfying the request.
func (d *Daemon) updateAndStartAsync(forceGitPull, wasRunning bool) {
	go func() {
		if !d.reloadMu.TryLock() {
			// A reload is already running and will pick up the freshly-pulled
			// bundle, so the update-and-start intent is satisfied. Log it so an
			// operator wondering "why didn't my start do anything?" can see the
			// request was coalesced rather than dropped.
			slog.Info("Update-and-start coalesced into in-progress reload")
			return
		}
		defer d.reloadMu.Unlock()
		if err := d.doReloadEx(forceGitPull, !wasRunning); err != nil {
			slog.Warn("Update and start failed", "err", err)
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
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			writeSSEEvent(w, evt)
			flusher.Flush()
			ticker.Reset(15 * time.Second)
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

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
