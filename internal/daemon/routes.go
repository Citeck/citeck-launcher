package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"gopkg.in/yaml.v3"
)

func (d *Daemon) handleDaemonStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.DaemonStatusDto{
		Running:    true,
		PID:        int64(os.Getpid()),
		Uptime:     time.Since(d.startTime).Milliseconds(),
		Version:    "dev",
		Workspace:  "daemon",
		SocketPath: d.socketPath,
	})
}

func (d *Daemon) handleDaemonShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Shutting down"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.shutdown()
	}()
}

func (d *Daemon) handleGetNamespace(w http.ResponseWriter, r *http.Request) {
	if d.runtime == nil {
		writeError(w, http.StatusNotFound, "no namespace configured")
		return
	}
	writeJSON(w, d.runtime.ToNamespaceDto())
}

func (d *Daemon) handleStartNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime, appDefs := d.runtime, d.appDefs
	d.configMu.RUnlock()
	if runtime == nil || appDefs == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	runtime.Start(appDefs)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace start requested"})
}

func (d *Daemon) handleStopNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime := d.runtime
	d.configMu.RUnlock()
	if runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	runtime.Stop()
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace stop requested"})
}

func (d *Daemon) handleReloadNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	if d.runtime == nil || d.nsConfig == nil || d.bundleDef == nil {
		d.configMu.RUnlock()
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	nsID := d.nsConfig.ID
	d.configMu.RUnlock()

	// Phase 1: slow I/O outside lock (config read, git pull, bundle resolution)
	nsCfg, err := namespace.LoadNamespaceConfig(config.ResolveNamespaceConfigPath(d.workspaceID, nsID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("reload config: %s", err.Error()))
		return
	}

	resolver := bundle.NewResolverWithAuth(config.DataDir(), makeTokenLookup(d.store))
	resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve bundle: %s", err.Error()))
		return
	}

	appfiles.ExtractTo(d.volumesBase)

	var genOpts namespace.GenerateOpts
	if d.runtime != nil {
		genOpts.DetachedApps = d.runtime.ManualStoppedApps()
	}
	genResp := namespace.Generate(nsCfg, resolveResult.Bundle, resolveResult.Workspace, genOpts)

	// Write generated files
	for filePath, content := range genResp.Files {
		destPath := filepath.Join(d.volumesBase, filePath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			slog.Error("Failed to create dir for generated file", "path", destPath, "err", err)
			continue
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			slog.Error("Failed to write generated file", "path", destPath, "err", err)
		}
	}

	// Phase 2: update shared state briefly under write lock
	d.configMu.Lock()
	d.nsConfig = nsCfg
	d.bundleDef = resolveResult.Bundle
	d.workspaceConfig = resolveResult.Workspace
	d.appDefs = genResp.Applications
	d.configMu.Unlock()

	if d.cloudCfgServer != nil {
		d.cloudCfgServer.UpdateConfig(genResp.CloudConfig)
	}
	d.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(d.workspaceConfig, d.store))

	// Phase 3: regenerate runtime (async stop + start)
	d.runtime.Regenerate(d.appDefs)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Reload requested"})
}

func (d *Daemon) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	tailStr := r.URL.Query().Get("tail")
	tail := 100
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}
	follow := r.URL.Query().Get("follow") == "true"

	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	logs := stripAnsi(rawLogs)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
}

// handleAppLogsFollow streams container logs using Docker follow with proper stdcopy demux.
func (d *Daemon) handleAppLogsFollow(w http.ResponseWriter, r *http.Request, containerID string, tail int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ctx := r.Context()
	reader, err := d.dockerClient.ContainerLogsFollow(ctx, containerID, tail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	// Use stdcopy to demux Docker multiplex headers, writing clean text to the response.
	// stdcopy.StdCopy blocks until the reader is closed (context cancellation or container stop).
	stdcopy.StdCopy(flushWriter{w, flusher}, flushWriter{w, flusher}, reader)
}

// flushWriter wraps an http.ResponseWriter to flush after every write.
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	fw.f.Flush()
	return n, err
}

func (d *Daemon) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if err := d.runtime.RestartApp(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Restart requested for %s", name)})
}

func (d *Daemon) handleAppStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	if d.findApp(name) == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if err := d.runtime.StopApp(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s stopped", name)})
}

func (d *Daemon) handleAppStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}
	if app.Status == namespace.AppStatusRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf("app %q is already running", name))
		return
	}

	if err := d.runtime.RestartApp(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s start requested", name)})
}

func (d *Daemon) handleAppInspect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var ports []string
	for containerPort, bindings := range inspect.NetworkSettings.Ports {
		for _, b := range bindings {
			ports = append(ports, fmt.Sprintf("%s:%s/%s", b.HostPort, containerPort.Port(), containerPort.Proto()))
		}
	}

	var volumes []string
	for _, m := range inspect.Mounts {
		volumes = append(volumes, fmt.Sprintf("%s:%s", m.Source, m.Destination))
	}

	var envVars []string
	envVars = append(envVars, inspect.Config.Env...)

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
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	var req api.ExecRequestDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	execCtx, execCancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer execCancel()
	output, exitCode, err := d.dockerClient.ExecInContainer(execCtx, app.ContainerID, req.Command)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, api.ExecResultDto{
		ExitCode: int64(exitCode),
		Output:   output,
	})
}

func (d *Daemon) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := d.activeConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("config file not found: %s", cfgPath))
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

func (d *Daemon) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := d.activeConfigPath()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Validate YAML by attempting to parse
	var testCfg namespace.NamespaceConfig
	if err := yaml.Unmarshal(body, &testCfg); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid YAML: %s", err.Error()))
		return
	}

	// Write file
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to write config: %s", err.Error()))
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := d.addSubscriber()
	defer d.removeSubscriber(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (d *Daemon) handleDaemonLogs(w http.ResponseWriter, r *http.Request) {
	logPath := config.DaemonLogPath()

	tailStr := r.URL.Query().Get("tail")
	tail := 200
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}

	// Read at most last 2MB of the file to avoid OOM on large logs
	const maxReadSize = 2 * 1024 * 1024
	f, err := os.Open(logPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("daemon log not found: %s", logPath))
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	readSize := stat.Size()
	if readSize > maxReadSize {
		f.Seek(-maxReadSize, io.SeekEnd)
		readSize = maxReadSize
	}
	data, err := io.ReadAll(io.LimitReader(f, readSize))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))
}

func (d *Daemon) handleSystemDump(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	dump := make(map[string]any)

	// Daemon info
	dump["daemon"] = map[string]any{
		"pid":     os.Getpid(),
		"uptime":  time.Since(d.startTime).Milliseconds(),
		"version": "dev",
		"socket":  d.socketPath,
	}

	// Namespace info
	if d.nsConfig != nil {
		dump["namespace"] = map[string]any{
			"id":     d.nsConfig.ID,
			"name":   d.nsConfig.Name,
			"bundle": d.nsConfig.BundleRef.String(),
		}
	}

	// Docker info
	if err := d.dockerClient.Ping(ctx); err != nil {
		dump["docker"] = map[string]any{"available": false, "error": err.Error()}
	} else {
		dump["docker"] = map[string]any{"available": true}
	}

	// Apps status
	if d.runtime != nil {
		apps := d.runtime.Apps()
		appList := make([]map[string]string, 0, len(apps))
		for _, app := range apps {
			appList = append(appList, map[string]string{
				"name":   app.Name,
				"status": string(app.Status),
				"image":  app.Def.Image,
			})
		}
		dump["apps"] = appList
	}

	w.Header().Set("Content-Disposition", "attachment; filename=system-dump.json")
	writeJSON(w, dump)
}

func (d *Daemon) volumesDir() string {
	return filepath.Join(d.volumesBase, "volumes")
}

func (d *Daemon) handleListVolumes(w http.ResponseWriter, _ *http.Request) {
	volDir := d.volumesDir()
	entries, err := os.ReadDir(volDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type volumeDto struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var result []volumeDto
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, volumeDto{
				Name: e.Name(),
				Path: filepath.Join(volDir, e.Name()),
			})
		}
	}
	if result == nil {
		result = []volumeDto{}
	}
	writeJSON(w, result)
}

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func (d *Daemon) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validNameRegex.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid volume name")
		return
	}
	volPath := filepath.Join(d.volumesDir(), name)
	if _, err := os.Stat(volPath); err != nil {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if err := os.RemoveAll(volPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s deleted", name)})
}

func (d *Daemon) handleGetAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}
	// Serialize ApplicationDef to YAML
	data, err := yaml.Marshal(app.Def)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

func (d *Daemon) handlePutAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
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

	newDef.Name = name
	if err := d.runtime.UpdateAppDef(name, newDef, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := d.runtime.RestartApp(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s config updated and restart requested", name)})
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var checks []api.HealthCheckDto

	// Docker check
	if err := d.dockerClient.Ping(ctx); err != nil {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "error", Message: err.Error()})
	} else {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "ok", Message: "Docker daemon is reachable"})
	}

	// App checks
	if d.runtime != nil {
		apps := d.runtime.Apps()
		running := 0
		for _, app := range apps {
			if app.Status == namespace.AppStatusRunning {
				running++
			}
		}

		status := "ok"
		if running < len(apps) {
			status = "warning"
		}
		checks = append(checks, api.HealthCheckDto{
			Name:    "containers",
			Status:  status,
			Message: fmt.Sprintf("%d/%d apps running", running, len(apps)),
		})

		for _, app := range apps {
			appStatus := "ok"
			if app.Status != namespace.AppStatusRunning {
				appStatus = "warning"
			}
			checks = append(checks, api.HealthCheckDto{
				Name:    "app:" + app.Name,
				Status:  appStatus,
				Message: string(app.Status),
			})
		}
	}

	healthy := true
	for _, c := range checks {
		if c.Status == "error" {
			healthy = false
			break
		}
	}

	writeJSON(w, api.HealthDto{Healthy: healthy, Checks: checks})
}

func (d *Daemon) findApp(name string) *namespace.AppRuntime {
	if d.runtime == nil {
		return nil
	}
	for _, app := range d.runtime.Apps() {
		if app.Name == name {
			return app
		}
	}
	return nil
}

// stripAnsi removes ANSI escape codes and normalizes tabs (matching Kotlin LogsUtils.normalizeMessage)
// Matches all CSI escape sequences (SGR colors, cursor movement, erase, etc.)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripAnsi(s string) string {
	s = ansiRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}
