package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/niceteck/citeck-launcher/internal/api"
	"github.com/niceteck/citeck-launcher/internal/bundle"
	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/namespace"
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
	if d.runtime == nil || d.appDefs == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	d.runtime.Start(d.appDefs)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace start requested"})
}

func (d *Daemon) handleStopNamespace(w http.ResponseWriter, r *http.Request) {
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	d.runtime.Stop()
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace stop requested"})
}

func (d *Daemon) handleReloadNamespace(w http.ResponseWriter, r *http.Request) {
	if d.runtime == nil || d.nsConfig == nil || d.bundleDef == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
	// Re-read config from disk
	nsCfg, err := namespace.LoadNamespaceConfig(config.NamespaceConfigPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("reload config: %s", err.Error()))
		return
	}
	d.nsConfig = nsCfg

	// Re-resolve bundle
	resolver := bundle.NewResolver(config.DataDir())
	resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve bundle: %s", err.Error()))
		return
	}
	d.bundleDef = resolveResult.Bundle

	// Re-generate namespace
	genResp := namespace.Generate(nsCfg, d.bundleDef, resolveResult.Workspace)
	d.appDefs = genResp.Applications

	// Regenerate runtime (stop + start with new config)
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

	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	rawLogs, err := d.dockerClient.ContainerLogs(context.Background(), app.ContainerID, tail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Strip ANSI escape codes and normalize
	logs := stripAnsi(rawLogs)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
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

	// RestartApp handles both starting a stopped app and restarting a running one
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

	inspect, err := d.dockerClient.InspectContainer(context.Background(), app.ContainerID)
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

	output, exitCode, err := d.dockerClient.ExecInContainer(context.Background(), app.ContainerID, req.Command)
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
	cfgPath := config.NamespaceConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("config file not found: %s", cfgPath))
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

func (d *Daemon) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := config.NamespaceConfigPath()

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
	data, err := os.ReadFile(logPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("daemon log not found: %s", logPath))
		return
	}

	// Return last N lines
	tailStr := r.URL.Query().Get("tail")
	tail := 200
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))
}

func (d *Daemon) handleSystemDump(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
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

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
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
			if app.Status != "RUNNING" {
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
var ansiRegex = regexp.MustCompile(`\x1b\[[\d;]*m`)

func stripAnsi(s string) string {
	s = ansiRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}
