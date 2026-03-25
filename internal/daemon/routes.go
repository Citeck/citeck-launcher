package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/niceteck/citeck-launcher/internal/namespace"

	"github.com/niceteck/citeck-launcher/internal/api"
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
	if d.runtime == nil {
		writeError(w, http.StatusBadRequest, "no namespace configured")
		return
	}
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
	// TODO: Re-read config, re-generate, regenerate runtime
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

	logs, err := d.dockerClient.ContainerLogs(context.Background(), app.ContainerID, tail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
}

func (d *Daemon) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	app := d.findApp(name)
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	// TODO: Implement restart logic (stop + start)
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Restart requested for %s", name)})
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
