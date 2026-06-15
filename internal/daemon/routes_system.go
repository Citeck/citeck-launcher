package daemon

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
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
	act := d.active()
	if nsCfg := act.nsConfig; nsCfg != nil {
		dump["namespace"] = map[string]any{
			"id":     nsCfg.ID,
			"name":   nsCfg.Name,
			"bundle": nsCfg.BundleRef.String(),
		}
	}
	if act.dockerClient == nil {
		// Zero-value Daemon (tests) — production always sets the client.
		dump["docker"] = map[string]any{"available": false, "error": "no docker client"}
	} else if err := act.dockerClient.Ping(ctx); err != nil {
		dump["docker"] = map[string]any{"available": false, "error": err.Error()}
	} else {
		dump["docker"] = map[string]any{"available": true}
	}
	if act.runtime != nil {
		apps := act.runtime.Apps()
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
		// nsConfig is replaced wholesale on reload/activate (never mutated in
		// place), so marshaling from the snapshot copy is race-free.
		var nsCfgYAML []byte
		if nsCfg := d.active().nsConfig; nsCfg != nil {
			masked := maskNamespaceConfigSecrets(nsCfg)
			nsCfgYAML, _ = namespace.MarshalNamespaceConfig(masked)
		}
		// daemon.yml goes into the ZIP only when the file actually exists on
		// disk: marshaling the in-memory config would fabricate a synthetic
		// daemon.yml out of pure defaults (typical on desktop, which never
		// writes one), misleading whoever reads the dump. When present, emit
		// the EFFECTIVE (loaded) config rather than raw file bytes — but mask
		// an explicit api_auth.token so the ZIP can be shared without leaking
		// API access.
		var daemonCfgYAML []byte
		if _, statErr := os.Stat(config.DaemonConfigPath()); statErr == nil {
			cfgCopy := d.daemonCfg
			if cfgCopy.APIAuth.Token != "" {
				cfgCopy.APIAuth.Token = "***REDACTED***"
			}
			daemonCfgYAML, _ = yaml.Marshal(cfgCopy)
		}
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

	// goroutine-dump.txt — pprof debug=2 emits ThreadInfo-like per-goroutine
	// stacks (state, locks, full frames). Kotlin parity: SystemDumpUtils.kt
	// writes `thread-dump.txt` via ThreadMXBean.dumpAllThreads; the closest Go
	// analog is the goroutine profile.
	if fw, err := zw.Create("goroutine-dump.txt"); err == nil {
		_ = pprof.Lookup("goroutine").WriteTo(fw, 2)
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

	// Per-app logs — one snapshot so runtime and docker client agree.
	if act := d.active(); act.runtime != nil && act.dockerClient != nil {
		dc := act.dockerClient
		for _, app := range act.runtime.Apps() {
			if app.ContainerID == "" {
				continue
			}
			logs, err := dc.ContainerLogs(ctx, app.ContainerID, 500)
			if err != nil {
				continue
			}
			fname := fmt.Sprintf("logs/%s.log", app.Name)
			if fw, err := zw.Create(fname); err == nil {
				// Strip ANSI color codes (Spring Boot emits SGR sequences) so the
				// dumped .log files are readable in a plain text editor — same
				// normalization the live log viewer already applies.
				_, _ = fw.Write([]byte(stripAnsi(logs)))
			}
		}
	}
}

//nolint:nestif // health aggregation checks multiple subsystems with per-app status roll-up
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var checks []api.HealthCheckDto

	// One snapshot for bundleError + dockerClient + runtime.
	act := d.active()

	// Bundle check
	if act.bundleError != "" {
		checks = append(checks, api.HealthCheckDto{Name: "bundle", Status: "error", Message: "Bundle resolution failed: " + act.bundleError})
	}

	// Docker check
	if err := act.dockerClient.Ping(ctx); err != nil {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "error", Message: err.Error()})
	} else {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "ok", Message: "Docker daemon is reachable"})
	}

	// Determine overall status: healthy / degraded / unhealthy
	overallStatus := "healthy"

	if act.runtime != nil {
		apps := act.runtime.Apps()
		running, failed := 0, 0
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := act.runtime.Status()

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

	// SSE pipeline health — one consistent snapshot for all SSE gauges.
	sse := d.sseStatsSnapshot()
	fmt.Fprintf(&b, "# HELP citeck_sse_subscribers Current SSE subscriber count.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_subscribers gauge\n")
	fmt.Fprintf(&b, "citeck_sse_subscribers %d\n", sse.Subscribers)
	fmt.Fprintf(&b, "# HELP citeck_sse_event_seq Last assigned SSE event sequence number (total events published).\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_event_seq counter\n")
	fmt.Fprintf(&b, "citeck_sse_event_seq %d\n", sse.EventSeq)
	fmt.Fprintf(&b, "# HELP citeck_sse_ring_events Events currently held in the SSE replay ring.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_ring_events gauge\n")
	fmt.Fprintf(&b, "citeck_sse_ring_events %d\n", sse.RingLen)
	fmt.Fprintf(&b, "# HELP citeck_sse_ring_capacity SSE replay ring capacity.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_ring_capacity gauge\n")
	fmt.Fprintf(&b, "citeck_sse_ring_capacity %d\n", sse.RingCap)
	if len(sse.QueueLens) > 0 {
		fmt.Fprintf(&b, "# HELP citeck_sse_subscriber_pending Undelivered events queued per SSE subscriber (high values precede drops).\n")
		fmt.Fprintf(&b, "# TYPE citeck_sse_subscriber_pending gauge\n")
		for i, qlen := range sse.QueueLens {
			fmt.Fprintf(&b, "citeck_sse_subscriber_pending{subscriber=\"%d\"} %d\n", i, qlen)
		}
	}

	// One d.active() snapshot for namespace identity + runtime roll-ups.
	act := d.active()
	if nsCfg := act.nsConfig; nsCfg != nil {
		fmt.Fprintf(&b, "# HELP citeck_namespace_info Active namespace identity (value is always 1).\n")
		fmt.Fprintf(&b, "# TYPE citeck_namespace_info gauge\n")
		fmt.Fprintf(&b, "citeck_namespace_info{id=\"%s\",name=\"%s\"} 1\n", promEscape(nsCfg.ID), promEscape(nsCfg.Name))
	}

	if rt := act.runtime; rt != nil {
		apps := rt.Apps()
		running, failed, total := 0, 0, len(apps)
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := string(rt.Status())
		fmt.Fprintf(&b, "# HELP citeck_namespace_status Current namespace status (1=active).\n")
		fmt.Fprintf(&b, "# TYPE citeck_namespace_status gauge\n")
		for _, s := range []string{api.NsStatusStopped, api.NsStatusStarting, api.NsStatusRunning, api.NsStatusStopping, api.NsStatusStalled} {
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

	// SSE dropped events (from the same snapshot as the gauges above)
	fmt.Fprintf(&b, "# HELP citeck_sse_events_dropped_total Total SSE events dropped due to slow consumers.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_events_dropped_total counter\n")
	fmt.Fprintf(&b, "citeck_sse_events_dropped_total %d\n", sse.Dropped)

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
	runtime := d.active().runtime
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
	diagDir := filepath.Join(d.activeVolumesBase(), "diagnostics") + string(os.PathSeparator)
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
	_, _ = w.Write(data) //nolint:gosec // G705 XSS taint: Content-Type is text/plain, not HTML
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
