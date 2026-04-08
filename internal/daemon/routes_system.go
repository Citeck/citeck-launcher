package daemon

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"gopkg.in/yaml.v3"
)

func (d *Daemon) buildDumpData(ctx context.Context) map[string]any {
	dump := make(map[string]any)
	dump["daemon"] = map[string]any{
		"pid":     os.Getpid(),
		"uptime":  time.Since(d.startTime).Milliseconds(),
		"version": d.version,
		"socket":  d.socketPath,
	}
	d.configMu.RLock()
	nsCfg := d.nsConfig
	d.configMu.RUnlock()
	if nsCfg != nil {
		dump["namespace"] = map[string]any{
			"id":     nsCfg.ID,
			"name":   nsCfg.Name,
			"bundle": nsCfg.BundleRef.String(),
		}
	}
	if err := d.dockerClient.Ping(ctx); err != nil {
		dump["docker"] = map[string]any{"available": false, "error": err.Error()}
	} else {
		dump["docker"] = map[string]any{"available": true}
	}
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
	return dump
}

func (d *Daemon) handleSystemDump(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	dump := d.buildDumpData(ctx)

	if r.URL.Query().Get("format") == "zip" {
		// Marshal configs under lock to avoid data races (slices/maps are reference types)
		d.configMu.RLock()
		var nsCfgYAML []byte
		if d.nsConfig != nil {
			masked := maskNamespaceConfigSecrets(d.nsConfig)
			nsCfgYAML, _ = namespace.MarshalNamespaceConfig(masked)
		}
		daemonCfgYAML, _ := yaml.Marshal(d.daemonCfg)
		d.configMu.RUnlock()
		d.writeSystemDumpZip(ctx, w, dump, nsCfgYAML, daemonCfgYAML)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=system-dump.json")
	writeJSON(w, dump)
}

func (d *Daemon) writeSystemDumpZip(ctx context.Context, w http.ResponseWriter, dump map[string]any, nsCfgYAML, daemonCfgYAML []byte) {
	// Extend write deadline for potentially large ZIP with per-app logs
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=system-dump.zip")

	zw := zip.NewWriter(w)
	defer zw.Close()

	// system-info.json
	if infoData, err := json.MarshalIndent(dump, "", "  "); err == nil {
		if fw, err := zw.Create("system-info.json"); err == nil {
			_, _ = fw.Write(infoData)
		}
	}

	// namespace.yml (pre-marshaled under configMu lock)
	if len(nsCfgYAML) > 0 {
		if fw, err := zw.Create("namespace.yml"); err == nil {
			_, _ = fw.Write(nsCfgYAML)
		}
	}

	// daemon.yml (pre-marshaled under configMu lock)
	if len(daemonCfgYAML) > 0 {
		if fw, err := zw.Create("daemon.yml"); err == nil {
			_, _ = fw.Write(daemonCfgYAML)
		}
	}

	// Daemon logs (daemon.log + rotated variants)
	const maxDaemonLogSize = 2 * 1024 * 1024 // 2MB cap per file
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		logFile := config.DaemonLogPath() + suffix
		data, err := os.ReadFile(logFile) //nolint:gosec // G304: logFile path is derived from internal config
		if err != nil {
			continue
		}
		if len(data) > maxDaemonLogSize {
			data = data[len(data)-maxDaemonLogSize:]
		}
		fname := "daemon-logs/" + filepath.Base(logFile)
		if fw, err := zw.Create(fname); err == nil {
			_, _ = fw.Write(data)
		}
	}

	// Per-app logs
	if d.runtime != nil {
		for _, app := range d.runtime.Apps() {
			if app.ContainerID == "" {
				continue
			}
			logs, err := d.dockerClient.ContainerLogs(ctx, app.ContainerID, 500)
			if err != nil {
				continue
			}
			fname := fmt.Sprintf("logs/%s.log", app.Name)
			if fw, err := zw.Create(fname); err == nil {
				_, _ = fw.Write([]byte(logs))
			}
		}
	}
}

//nolint:nestif // health aggregation checks multiple subsystems with per-app status roll-up
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var checks []api.HealthCheckDto

	// Bundle check
	d.configMu.RLock()
	bundleErr := d.bundleError
	d.configMu.RUnlock()
	if bundleErr != "" {
		checks = append(checks, api.HealthCheckDto{Name: "bundle", Status: "error", Message: "Bundle resolution failed: " + bundleErr})
	}

	// Docker check
	if err := d.dockerClient.Ping(ctx); err != nil {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "error", Message: err.Error()})
	} else {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "ok", Message: "Docker daemon is reachable"})
	}

	// Determine overall status: healthy / degraded / unhealthy
	overallStatus := "healthy"

	if d.runtime != nil {
		apps := d.runtime.Apps()
		running, failed := 0, 0
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := d.runtime.Status()

		// Determine container-level check status
		containerStatus := "ok"
		if len(apps) == 0 {
			// Namespace stopped or no apps — not a health failure
			containerStatus = "ok"
		} else if running == 0 {
			containerStatus = "error"
		} else if running < len(apps) {
			containerStatus = "warning"
		}
		checks = append(checks, api.HealthCheckDto{
			Name:    "containers",
			Status:  containerStatus,
			Message: fmt.Sprintf("%d/%d apps running, %d failed", running, len(apps), failed),
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

		// Overall status
		if nsStatus == namespace.NsStatusStalled || (len(apps) > 0 && running == 0) {
			overallStatus = "unhealthy"
		} else if failed > 0 || (len(apps) > 0 && running < len(apps)) {
			overallStatus = "degraded"
		}
	}

	// Check for critical check errors — escalate to unhealthy
	for _, c := range checks {
		if c.Status == "error" {
			overallStatus = "unhealthy"
			break
		}
	}

	writeJSON(w, api.HealthDto{Status: overallStatus, Healthy: overallStatus == "healthy", Checks: checks})
}

func (d *Daemon) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	uptimeSeconds := time.Since(d.startTime).Seconds()
	fmt.Fprintf(&b, "# HELP citeck_uptime_seconds Daemon uptime in seconds.\n")
	fmt.Fprintf(&b, "# TYPE citeck_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "citeck_uptime_seconds %.1f\n", uptimeSeconds)

	d.eventMu.Lock()
	sseCount := len(d.eventSubs)
	d.eventMu.Unlock()
	fmt.Fprintf(&b, "# HELP citeck_sse_subscribers Current SSE subscriber count.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_subscribers gauge\n")
	fmt.Fprintf(&b, "citeck_sse_subscribers %d\n", sseCount)

	if d.runtime != nil {
		apps := d.runtime.Apps()
		running, failed, total := 0, 0, len(apps)
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := string(d.runtime.Status())
		fmt.Fprintf(&b, "# HELP citeck_namespace_status Current namespace status (1=active).\n")
		fmt.Fprintf(&b, "# TYPE citeck_namespace_status gauge\n")
		for _, s := range []string{"STOPPED", "STARTING", "RUNNING", "STOPPING", "STALLED"} {
			val := 0
			if s == nsStatus {
				val = 1
			}
			fmt.Fprintf(&b, "citeck_namespace_status{status=\"%s\"} %d\n", s, val)
		}

		fmt.Fprintf(&b, "# HELP citeck_apps_total Total number of apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_total gauge\n")
		fmt.Fprintf(&b, "citeck_apps_total %d\n", total)
		fmt.Fprintf(&b, "# HELP citeck_apps_running Number of running apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_running gauge\n")
		fmt.Fprintf(&b, "citeck_apps_running %d\n", running)
		fmt.Fprintf(&b, "# HELP citeck_apps_failed Number of failed apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_failed gauge\n")
		fmt.Fprintf(&b, "citeck_apps_failed %d\n", failed)

		// Per-app status gauge
		fmt.Fprintf(&b, "# HELP citeck_app_status Per-app status (1=running, 0=not running).\n")
		fmt.Fprintf(&b, "# TYPE citeck_app_status gauge\n")
		for _, app := range apps {
			val := 0
			if app.Status == namespace.AppStatusRunning {
				val = 1
			}
			fmt.Fprintf(&b, "citeck_app_status{app=\"%s\",status=\"%s\"} %d\n", promEscape(app.Name), promEscape(string(app.Status)), val)
		}
	}

	// Build info
	fmt.Fprintf(&b, "# HELP citeck_build_info Build version and metadata.\n")
	fmt.Fprintf(&b, "# TYPE citeck_build_info gauge\n")
	fmt.Fprintf(&b, "citeck_build_info{version=\"%s\"} 1\n", promEscape(d.version))

	// HTTP request metrics
	httpMetrics.writePrometheus(&b)

	// SSE dropped events
	fmt.Fprintf(&b, "# HELP citeck_sse_events_dropped_total Total SSE events dropped due to slow consumers.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_events_dropped_total counter\n")
	fmt.Fprintf(&b, "citeck_sse_events_dropped_total %d\n", d.sseDropped.Load())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// promEscape escapes a label value for Prometheus text exposition format.
// Only \, \n, and " need escaping per the spec.
func promEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func (d *Daemon) handleRestartEvents(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime := d.runtime
	d.configMu.RUnlock()
	if runtime == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	events := runtime.RestartEvents()
	dtos := make([]api.RestartEventDto, len(events))
	for i, e := range events {
		dtos[i] = api.RestartEventDto{
			Timestamp:   e.Timestamp,
			App:         e.App,
			Reason:      e.Reason,
			Detail:      e.Detail,
			Diagnostics: e.Diagnostics,
		}
	}
	writeJSON(w, dtos)
}

func (d *Daemon) handleDiagnosticsFile(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "missing path parameter")
		return
	}
	diagDir := filepath.Join(d.volumesBase, "diagnostics") + string(os.PathSeparator)
	absPath, err := filepath.Abs(filePath)
	if err != nil || !strings.HasPrefix(absPath, diagDir) {
		writeErrorCode(w, http.StatusForbidden, api.ErrCodeInvalidRequest, "path outside diagnostics directory")
		return
	}
	data, err := os.ReadFile(absPath) //nolint:gosec // G304: path validated via HasPrefix(absPath, diagDir)
	if err != nil {
		writeError(w, http.StatusNotFound, "diagnostics file not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(absPath)))
	_, _ = w.Write(data)
}

// maskNamespaceConfigSecrets returns a shallow copy of the config with passwords in
// authentication.users replaced by "***". Does not mutate the original config.
func maskNamespaceConfigSecrets(cfg *namespace.Config) *namespace.Config {
	out := *cfg
	if len(out.Authentication.Users) > 0 {
		masked := make([]string, len(out.Authentication.Users))
		for i, u := range out.Authentication.Users {
			if idx := strings.Index(u, ":"); idx >= 0 {
				masked[i] = u[:idx+1] + "***"
			} else {
				masked[i] = u
			}
		}
		out.Authentication.Users = masked
	}
	return &out
}
