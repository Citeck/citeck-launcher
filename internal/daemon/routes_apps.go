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
	_, _ = w.Write([]byte(logs)) //nolint:gosec // G705: plain text log output, not HTML
}

// handleAppLogsFollow streams container logs using Docker follow with proper stdcopy demux.
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
	reader, err := d.dockerClient.ContainerLogsFollow(ctx, containerID, tail)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	// Use stdcopy to demux Docker multiplex headers, writing clean text to the response.
	// stdcopy.StdCopy blocks until the reader is closed (context cancellation or container stop).
	_, _ = stdcopy.StdCopy(flushWriter{w, flusher}, flushWriter{w, flusher}, reader)
}

// flushWriter wraps an http.ResponseWriter to flush after every write.
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("write: %w", err)
	}
	fw.f.Flush()
	return n, nil
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
	// Serialize ApplicationDef to YAML
	data, err := yaml.Marshal(app.Def)
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

	// Defense-in-depth: only allow safe mutable fields (environments, resources,
	// startupConditions, livenessProbe, stopTimeout).
	// Structural fields (image, cmd, ports, volumes) are locked to the original
	// definition to prevent container escape.
	oldDef := app.Def
	newDef.Name = name
	newDef.Image = oldDef.Image
	newDef.ImageDigest = oldDef.ImageDigest
	newDef.Cmd = oldDef.Cmd
	newDef.Ports = oldDef.Ports
	newDef.Volumes = oldDef.Volumes
	newDef.VolumesContentHash = oldDef.VolumesContentHash
	newDef.InitContainers = oldDef.InitContainers
	newDef.InitActions = oldDef.InitActions
	newDef.NetworkAliases = oldDef.NetworkAliases
	newDef.Kind = oldDef.Kind
	newDef.IsInit = oldDef.IsInit
	newDef.DependsOn = oldDef.DependsOn
	newDef.ShmSize = oldDef.ShmSize

	if err := d.runtime.UpdateAppDef(name, newDef, true); err != nil {
		writeInternalError(w, err)
		return
	}
	if err := d.runtime.RestartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s config updated and restart requested", name)})
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

	// Collect bind-mounted files from relative bind mounts (./app/... etc.)
	var files []string
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPath := parts[0]
		if !strings.HasPrefix(hostPath, "./") {
			continue
		}
		// Resolve and check if the path is a regular file (not a directory)
		absPath := filepath.Join(d.volumesBase, hostPath[2:])
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			files = append(files, hostPath)
		}
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
	_, _ = w.Write(data) //nolint:gosec // G705: raw file content response, not HTML
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
	if err := fsutil.AtomicWriteFile(absPath, body, 0o644); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "File updated"})
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

func isAppBindMount(app *namespace.AppRuntime, relPath string) bool {
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) >= 2 && parts[0] == relPath {
			return true
		}
	}
	return false
}
