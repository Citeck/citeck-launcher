package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/docker/docker/pkg/stdcopy"
	"gopkg.in/yaml.v3"
)

func (d *Daemon) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	tail := parseTailParam(r, 100, 10000)
	follow := r.URL.Query().Get("follow") == "true"

	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}
	// Container hasn't been created yet (PULL_FAILED / START_FAILED before
	// docker create, or a freshly-loaded namespace that never started).
	// Return an empty body instead of bouncing the request through the Docker
	// client just to surface "invalid container name or ID: value is empty"
	// as a 500 in the log.
	if app.ContainerID == "" {
		w.Header().Set("Content-Type", "text/plain")
		return
	}

	if follow {
		d.handleAppLogsFollow(w, r, app.ContainerID, tail)
		return
	}

	logCtx, logCancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer logCancel()
	rawLogs, err := d.dockerClient.ContainerLogs(logCtx, app.ContainerID, tail)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	logs := stripAnsi(rawLogs)
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(logs)) //nolint:gosec // G705 XSS taint: Content-Type is text/plain, not HTML
}

// handleAppLogsFollow streams container logs: the backlog first, then a live
// follow.
//
// The backlog is fetched via the NON-follow path on purpose. Docker's
// Follow=true + Tail=N stream desyncs stdcopy when tail < total lines — it
// misframes the partial backlog, reads a bogus frame length, and stalls after
// ~100 lines (this truncated eproc's 500-line tail to ~111). The non-follow
// ContainerLogs path demuxes the exact same Tail=N backlog correctly (and has a
// TTY fallback), so we use it for the backlog and follow only for live output,
// started with Tail=0 so there is no backlog for stdcopy to desync on.
func (d *Daemon) handleAppLogsFollow(w http.ResponseWriter, r *http.Request, containerID string, tail int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable write deadline for long-lived log stream
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	ctx := r.Context()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	// Backlog (last `tail` lines) via the robust non-follow demux.
	if tail != 0 {
		if backlog, err := d.dockerClient.ContainerLogs(ctx, containerID, tail); err == nil && backlog != "" {
			_, _ = w.Write([]byte(stripAnsi(backlog))) //nolint:gosec // G705 XSS taint: Content-Type is text/plain, not HTML
			flusher.Flush()
		}
	}

	// Live tail: Tail=0 → docker emits only NEW lines from the current position,
	// so the follow stream starts frame-aligned (no mid-backlog stdcopy desync).
	// A line emitted in the gap between the backlog read and this call is a rare
	// single-line seam — acceptable for a log viewer, and far better than the
	// silent truncation it replaces.
	reader, err := d.dockerClient.ContainerLogsFollow(ctx, containerID, 0)
	if err != nil {
		// Backlog already written; we can't change the status code now.
		return
	}
	defer reader.Close()

	// Use stdcopy to demux Docker multiplex headers, writing clean text to the response.
	// stdcopy.StdCopy blocks until the reader is closed (context cancellation or container stop).
	stdoutW := &flushWriter{w: w, f: flusher}
	stderrW := &flushWriter{w: w, f: flusher}
	_, _ = stdcopy.StdCopy(stdoutW, stderrW, reader)
}

// flushWriter wraps an http.ResponseWriter to flush after every write and to
// strip ANSI escape codes / normalize tabs the same way the non-follow path
// (handleAppLogs → stripAnsi) does. Without this, Java apps that print to
// stdout with ANSI color SGR sequences ("\x1b[32mINFO\x1b[0m") leak raw
// "[32mINFO[0m" into the log viewer when follow=true.
//
// Strip is byte-buffered because an SGR sequence can straddle two stdcopy
// chunks; pendingEsc holds an unfinished "\x1b[..." run between writes.
type flushWriter struct {
	w          http.ResponseWriter
	f          http.Flusher
	pendingEsc []byte
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	cleaned, leftover := stripAnsiBytes(p, fw.pendingEsc)
	fw.pendingEsc = leftover
	if len(cleaned) > 0 {
		if _, err := fw.w.Write(cleaned); err != nil {
			return 0, fmt.Errorf("write: %w", err)
		}
	}
	fw.f.Flush()
	return len(p), nil
}

// stripAnsiBytes removes CSI escape codes from b (carrying over any unfinished
// sequence from `carry`), converts tabs to four spaces, and returns the
// cleaned slice plus a new carry to feed into the next call.
//
// CSI grammar: ESC '[' (parameter bytes 0x30–0x3f)* (intermediate 0x20–0x2f)*
// (final byte 0x40–0x7e). We treat parameter and intermediate as one merged
// range 0x20–0x3f to keep the loop tight; malformed sequences (ESC followed
// by something other than '[') drop just the ESC byte.
func stripAnsiBytes(b, carry []byte) (cleaned, leftover []byte) {
	if len(carry) > 0 {
		b = append(carry, b...)
	}
	out := make([]byte, 0, len(b))
	i := 0
	for i < len(b) {
		c := b[i]
		switch c {
		case 0x1b:
			if i+1 >= len(b) {
				return out, b[i:]
			}
			if b[i+1] != '[' {
				i++
				continue
			}
			j := i + 2
			for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f {
				j++
			}
			if j >= len(b) {
				return out, b[i:]
			}
			if b[j] >= 0x40 && b[j] <= 0x7e {
				i = j + 1
				continue
			}
			// Malformed terminator — skip just the ESC.
			i++
		case '\t':
			out = append(out, ' ', ' ', ' ', ' ')
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, nil
}

// handleAppsRetryPullFailed re-queues all PULL_FAILED apps for a fresh pull
// attempt. Triggered by the Web UI after the user saves registry credentials
// via RegistryCredentialsDialog so the new secret is picked up without waiting
// for the auto-retry backoff window. The underlying RetryPullFailedApps call
// is a no-op when no apps are in PULL_FAILED — safe to invoke unconditionally.
func (d *Daemon) handleAppsRetryPullFailed(w http.ResponseWriter, _ *http.Request) {
	if !d.requireRuntime(w) {
		return
	}
	count := d.runtime.RetryPullFailedApps()
	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Retry requested for %d pull-failed app(s)", count),
	})
}

func (d *Daemon) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	if err := d.runtime.RestartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Restart requested for %s", name)})
}

func (d *Daemon) handleAppStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	if d.findApp(name) == nil {
		writeAppNotFound(w, name)
		return
	}

	if err := d.runtime.StopApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s stopped", name)})
}

func (d *Daemon) handleAppStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}
	if app.Status == namespace.AppStatusRunning {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeAppAlreadyRunning, fmt.Sprintf("app %q is already running", name))
		return
	}

	if err := d.runtime.StartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s start requested", name)})
}

// handleClearAppRestartEvents wipes the restart-event log for a single app.
// Surfaced by the "clear" button in the app's right-drawer restart section.
func (d *Daemon) handleClearAppRestartEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	if d.findApp(name) == nil {
		writeAppNotFound(w, name)
		return
	}
	d.runtime.ClearRestartEvents(name)
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Restart events cleared for %s", name)})
}

func (d *Daemon) handleAppInspect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	if app.ContainerID == "" {
		writeJSON(w, api.AppInspectDto{
			Name:   app.Name,
			Status: string(app.Status),
			Image:  app.Def.Image,
		})
		return
	}

	inspCtx, inspCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer inspCancel()
	inspect, err := d.dockerClient.InspectContainer(inspCtx, app.ContainerID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var ports []string
	for containerPort, bindings := range inspect.NetworkSettings.Ports {
		for _, b := range bindings {
			ports = append(ports, fmt.Sprintf("%s:%s/%s", b.HostPort, containerPort.Port(), containerPort.Proto()))
		}
	}

	volumes := make([]string, 0, len(inspect.Mounts))
	for _, m := range inspect.Mounts {
		volumes = append(volumes, fmt.Sprintf("%s:%s", m.Source, m.Destination))
	}

	envVars := make([]string, len(inspect.Config.Env))
	for i, e := range inspect.Config.Env {
		envVars[i] = api.MaskSecretEnv(e)
	}

	dto := api.AppInspectDto{
		Name:         app.Name,
		ContainerID:  app.ContainerID,
		Image:        inspect.Config.Image,
		Status:       string(app.Status),
		State:        inspect.State.Status,
		Ports:        ports,
		Volumes:      volumes,
		Env:          envVars,
		Labels:       inspect.Config.Labels,
		Network:      d.dockerClient.NetworkName(),
		RestartCount: inspect.RestartCount,
		StartedAt:    inspect.State.StartedAt,
	}

	if inspect.State.StartedAt != "" {
		if startedAt, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
			dto.Uptime = time.Since(startedAt).Milliseconds()
		}
	}

	writeJSON(w, dto)
}

func (d *Daemon) handleAppExec(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	// Limit request body to 64KB (command array doesn't need more)
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req api.ExecRequestDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	execCtx, execCancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer execCancel()
	output, exitCode, err := d.dockerClient.ExecInContainer(execCtx, app.ContainerID, req.Command)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Cap output at 1MB to prevent OOM
	const maxExecOutput = 1 << 20
	if len(output) > maxExecOutput {
		output = output[:maxExecOutput] + "\n... (output truncated at 1MB)"
	}

	writeJSON(w, api.ExecResultDto{
		ExitCode: int64(exitCode),
		Output:   output,
	})
}

func (d *Daemon) handleGetAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}
	// Hide the runtime-internal cache fields from the editor view: the digest
	// is re-resolved from the image at every container start, and the volume
	// content hash is recomputed by the generator. Showing them suggests the
	// operator should manage them; ignoring them on PUT is the matching half.
	clean := app.Def
	clean.ImageDigest = ""
	clean.VolumesContentHash = ""
	data, err := yaml.Marshal(clean)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	_, _ = w.Write(data)
}

func (d *Daemon) handlePutAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var newDef appdef.ApplicationDef
	if err := yaml.Unmarshal(body, &newDef); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid YAML: %s", err.Error()))
		return
	}

	// Kotlin 1.x's AppCfgEditWindow let the operator edit any field in the
	// YAML — image, cmd, ports, volumes, environments, etc. — and the
	// runtime treated the result as the new ApplicationDef (with a
	// recreated container on hash change). Earlier we copied a defense-in-
	// depth reset of structural fields from a hardened-server mindset; in
	// a desktop launcher the operator IS the user, so the reset just made
	// it look like saves silently failed for any "wrong" field.
	//
	// Lock only the genuinely non-editable fields: the canonical Name (URL
	// keys the app). ImageDigest is a runtime cache (resolved at container
	// start from the image tag) so we always drop it on save; the next pull
	// resolves the correct digest for whatever image the user chose.
	// VolumesContentHash is recomputed by the generator on the next
	// regenerate — also a runtime cache, also always cleared.
	_ = app.Def // oldDef no longer needed — every formerly-pinned field is now editable.
	newDef.Name = name
	newDef.ImageDigest = ""
	newDef.VolumesContentHash = ""

	if err := d.runtime.UpdateAppDef(name, newDef, true); err != nil {
		writeInternalError(w, err)
		return
	}
	// While the namespace is stopped the edit is persisted as an override and
	// applies on the next start, so we must NOT call RestartApp — it would
	// error with "runtime not started" and surface a spurious failure for a
	// save that actually succeeded. When running, restart to apply immediately.
	msg := fmt.Sprintf("App %s config updated and restart requested", name)
	if d.runtime.Status() == namespace.NsStatusStopped {
		msg = fmt.Sprintf("App %s config saved; applies on next start", name)
	} else if err := d.runtime.RestartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: msg})
}

// handleResetAppConfig clears any user-edited ApplicationDef override for the
// app so the original generated definition is restored. Mirrors Kotlin's
// `AppCfgEditWindow` Reset button (resume with `null`).
func (d *Daemon) handleResetAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.findApp(name) == nil {
		writeAppNotFound(w, name)
		return
	}
	if err := d.runtime.ResetAppDef(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s config reset to default", name)})
}

func (d *Daemon) handleListAppFiles(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	// Collect bind-mounted files from relative bind mounts (./app/... etc.).
	// `path` keeps the human-readable "./app/..." form for backwards-compat
	// with existing UI code; `edited` reflects whether the user has edited
	// the file via the Web UI (key stored without the leading "./").
	//
	// Bind-mount source can be a regular file OR a directory. For directory
	// mounts (typical for Spring webapps: `./app/<name>/props:/run/...`) we
	// walk the directory and emit each regular file inside — Kotlin 1.x
	// behavior the COG RMB menu relied on to surface application.yml etc.
	files := make([]api.AppFileDto, 0)
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPath := parts[0]
		if !strings.HasPrefix(hostPath, "./") {
			continue
		}
		absPath := filepath.Join(d.volumesBase, hostPath[2:])
		info, statErr := os.Stat(absPath) //nolint:gosec // G304: absPath derived from validated bind mount + volumesBase
		if statErr != nil {
			continue
		}
		if !info.IsDir() {
			edited := d.runtime != nil && d.runtime.IsFileEdited(hostPath[2:])
			files = append(files, api.AppFileDto{Path: hostPath, Edited: edited})
			continue
		}
		// Directory mount — walk it and surface every regular file inside.
		// The walker is bounded to the bind-mount root so it cannot escape
		// volumesBase even if a hostile bundle planted symlinks. We also cap
		// the result count: a pathological bundle (or operator typo) that
		// mounts `./` or another large directory would otherwise materialize
		// thousands of entries into the editor menu and the response JSON.
		// 500 is well above the legitimate range (Spring props ≈ 10, log /
		// data dirs reach a few dozen) and well below the worst-case noise.
		const dirMountFileCap = 500
		walkedHere := 0
		_ = filepath.WalkDir(absPath, func(child string, d2 os.DirEntry, walkErr error) error {
			if walkErr != nil || d2.IsDir() {
				return nil
			}
			if walkedHere >= dirMountFileCap {
				return filepath.SkipAll
			}
			rel, relErr := filepath.Rel(d.volumesBase, child)
			if relErr != nil {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			edited := d.runtime != nil && d.runtime.IsFileEdited(relSlash)
			files = append(files, api.AppFileDto{Path: "./" + relSlash, Edited: edited})
			walkedHere++
			return nil
		})
	}
	writeJSON(w, files)
}

func (d *Daemon) handleGetAppFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	filePath := r.PathValue("path")
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	// Validate path is a known bind mount
	relPath := "./" + filePath
	if !isAppBindMount(app, relPath) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q is not a bind mount of app %q", filePath, name))
		return
	}

	absPath := filepath.Join(d.volumesBase, filePath)
	if !isPathUnder(absPath, d.volumesBase) {
		writeError(w, http.StatusForbidden, "path outside workspace")
		return
	}
	data, err := os.ReadFile(absPath) //nolint:gosec // G304: absPath is validated against workspace root above
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data) //nolint:gosec // G705 XSS taint: Content-Type is text/plain, not HTML
}

func (d *Daemon) handlePutAppFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	filePath := r.PathValue("path")
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}

	relPath := "./" + filePath
	if !isAppBindMount(app, relPath) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("file %q is not a bind mount of app %q", filePath, name))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	absPath := filepath.Join(d.volumesBase, filePath)
	if !isPathUnder(absPath, d.volumesBase) {
		writeError(w, http.StatusForbidden, "path outside workspace")
		return
	}
	// Atomic: write the file AND mark it as edited under the same runtime
	// lock so a regenerate already in-flight can't read a stale edited map
	// and overwrite the user's content with the bundle template right after
	// the write. Falling back to the legacy two-step write+SetFileEdited if
	// the runtime is not available (server-mode test stubs).
	if d.runtime != nil {
		if err := d.runtime.WriteEditedFile(filePath, absPath, body); err != nil {
			writeInternalError(w, err)
			return
		}
	} else {
		if err := fsutil.AtomicWriteFile(absPath, body, 0o644); err != nil {
			writeInternalError(w, err)
			return
		}
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "File updated"})
}

// handleResetAppFile clears the user-edited flag for a single mounted
// bind-mount file and triggers a namespace reload so the original generator
// content is re-materialized on disk. Mirrors handleResetAppConfig.
//
// Path is taken from the `?path=` query string and MUST be an existing
// bind-mount of the named app. The canonical key (no leading "./") is what
// the runtime stores in editedFiles and what writeRuntimeFiles iterates over.
func (d *Daemon) handleResetAppFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeAppNotFound(w, name)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	// Normalize to the human-readable form for bind-mount validation, and
	// derive the canonical runtime key (no leading "./") for ResetEditedFile.
	cleanPath := strings.TrimPrefix(path, "./")
	relPath := "./" + cleanPath
	if !isAppBindMount(app, relPath) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q is not a bind mount of app %q", cleanPath, name))
		return
	}
	// Acquire reloadMu BEFORE clearing the in-memory edit flag so a concurrent
	// reload cannot read a stale editedFiles snapshot that still skips this
	// file. Without this, the user would see a 409 conflict yet the flag
	// would already be cleared — leaving the on-disk content stale until
	// the next manual reload.
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()
	if err := d.runtime.ResetEditedFile(name, cleanPath); err != nil {
		writeInternalError(w, err)
		return
	}
	// Trigger a reload so writeRuntimeFiles re-materializes the original
	// generator-supplied content; the on-disk file still has the user's
	// edit until this runs.
	if err := d.doReload(); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("File %s reset to default", cleanPath)})
}

func (d *Daemon) handleAppLockToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if !d.requireRuntime(w) {
		return
	}
	if d.findApp(name) == nil {
		writeAppNotFound(w, name)
		return
	}

	var body struct {
		Locked bool `json:"locked"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	d.runtime.SetAppLocked(name, body.Locked)
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s lock=%v", name, body.Locked)})
}

func (d *Daemon) findApp(name string) *namespace.AppRuntime {
	if d.runtime == nil {
		return nil
	}
	return d.runtime.FindApp(name)
}

func writeAppNotFound(w http.ResponseWriter, name string) {
	writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
}

// validateAppName checks if the name matches the valid pattern. Returns false and writes 400 if invalid.
func validateAppName(w http.ResponseWriter, name string) bool {
	if !safeIDPattern.MatchString(name) {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, fmt.Sprintf("invalid app name %q", name))
		return false
	}
	return true
}

// stripAnsi removes ANSI escape codes and normalizes tabs (matching Kotlin LogsUtils.normalizeMessage)
// Matches all CSI escape sequences (SGR colors, cursor movement, erase, etc.)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripAnsi(s string) string {
	s = ansiRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}

func isPathUnder(path, base string) bool {
	cleanPath := filepath.Clean(path)
	cleanBase := filepath.Clean(base)
	return strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator))
}

// isAppBindMount reports whether relPath (with leading "./") is reachable
// through one of the app's bind mounts. Accepts both:
//   - exact match for individual-file mounts ("./proxy/lua_oidc_full_access.lua:/...")
//   - any path under a directory mount ("./app/eapps/props:/..." matches
//     "./app/eapps/props/application-launcher.yml" and any deeper entry)
//
// The directory case is required for Spring webapps (props/ dir mount), which
// is what handleListAppFiles enumerates via filepath.WalkDir; without this
// the editor would refuse to read or write the files the menu surfaced.
//
// Paths are normalised via filepath.Clean and the directory-mount check is
// performed via filepath.Rel so a payload like "./app/eapps/props/../../etc/passwd"
// (which lexically starts with the host prefix but escapes it) is rejected.
func isAppBindMount(app *namespace.AppRuntime, relPath string) bool {
	cleanRel := filepath.Clean(relPath)
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) < 2 {
			continue
		}
		host := parts[0]
		cleanHost := filepath.Clean(strings.TrimSuffix(host, "/"))
		if cleanHost == cleanRel {
			return true
		}
		rel, err := filepath.Rel(cleanHost, cleanRel)
		if err != nil {
			continue
		}
		// rel == "." means equal (handled above); rel == ".." or starting
		// with "../" means cleanRel escapes the host directory.
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return true
	}
	return false
}
