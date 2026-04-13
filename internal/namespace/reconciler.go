package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/docker/docker/client"
)

// ReconcilerConfig holds reconciliation settings.
type ReconcilerConfig struct {
	Enabled         bool
	IntervalSeconds int
	LivenessEnabled bool
	LivenessPeriod  time.Duration
}

// DefaultReconcilerConfig returns the default reconciler settings.
func DefaultReconcilerConfig() ReconcilerConfig {
	return ReconcilerConfig{
		Enabled:         true,
		IntervalSeconds: 60,
		LivenessEnabled: true,
		LivenessPeriod:  30 * time.Second,
	}
}

// RunReconciler starts the reconciliation loop in a goroutine.
// It checks desired vs actual state and fixes discrepancies.
func (r *Runtime) RunReconciler(ctx context.Context, cfg ReconcilerConfig) {
	if !cfg.Enabled {
		return
	}

	r.reconcileWg.Go(func() {
		ticker := time.NewTicker(time.Duration(cfg.IntervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.reconcile(ctx)
			}
		}
	})

	if cfg.LivenessEnabled {
		r.reconcileWg.Go(func() {
			ticker := time.NewTicker(cfg.LivenessPeriod)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					r.checkLiveness(ctx)
				}
			}
		})
	}
}

// reconcile compares desired state (app definitions) with actual state (Docker containers).
func (r *Runtime) reconcile(ctx context.Context) {
	r.mu.RLock()
	if r.status != NsStatusRunning && r.status != NsStatusStalled {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	// Get actual containers — detect Docker daemon restart
	containers, err := r.docker.GetContainers(ctx)
	if err != nil {
		if client.IsErrConnectionFailed(err) {
			slog.Warn("Reconciler: Docker daemon unreachable, will retry next cycle")
		} else {
			slog.Warn("Reconciler: failed to list containers", "err", err)
		}
		return
	}

	// Build map of running containers by app name
	containersByApp := make(map[string]bool)
	for _, c := range containers {
		if appName, ok := c.Labels[docker.LabelAppName]; ok {
			if c.State == "running" {
				containersByApp[appName] = true
			}
		}
	}

	// Phase 1: collect candidate apps and container IDs under read lock
	type missingApp struct {
		name        string
		containerID string
	}
	var missing []missingApp

	r.mu.RLock()
	for name, app := range r.apps {
		if app.Status == AppStatusRunning && !containersByApp[name] {
			missing = append(missing, missingApp{name: name, containerID: app.ContainerID})
		}
	}
	r.mu.RUnlock()

	// Phase 2: inspect containers outside any lock (Docker API calls may take seconds)
	oomKilled := make(map[string]bool)
	for _, m := range missing {
		if m.containerID != "" {
			inspCtx, inspCancel := context.WithTimeout(ctx, 5*time.Second)
			info, inspErr := r.docker.InspectContainer(inspCtx, m.containerID)
			inspCancel()
			if inspErr == nil && info.State != nil && info.State.OOMKilled {
				oomKilled[m.name] = true
			}
		}
	}

	// Phase 3: re-acquire write lock, update state
	toRestart := make([]string, 0, len(missing))
	now := time.Now()

	r.mu.Lock()
	for _, m := range missing {
		app, ok := r.apps[m.name]
		if !ok || app.Status != AppStatusRunning || containersByApp[m.name] {
			continue // state changed while we were inspecting
		}
		var reason, detail string
		if oomKilled[m.name] {
			reason = "oom"
			detail = "container OOM killed"
			slog.Warn("Reconciler: container OOM killed, will restart", "app", m.name)
			r.emitEvent(api.EventDto{
				Type: "app_oom", Timestamp: time.Now().UnixMilli(),
				NamespaceID: r.nsID, AppName: m.name, After: "OOMKilled",
			})
		} else {
			reason = "crash"
			detail = "container disappeared"
			slog.Warn("Reconciler: container missing, will restart", "app", m.name)
		}
		r.incrementRestartCount(m.name)
		r.recordRestartEvent(RestartEvent{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			App:       m.name,
			Reason:    reason,
			Detail:    detail,
		})
		r.setAppStatus(app, AppStatusReadyToPull)
		toRestart = append(toRestart, m.name)
	}
	if len(missing) > 0 {
		r.persistState()
	}
	// Retry failed apps with exponential backoff (1m, 2m, 4m, 8m, max 10m)
	for name, app := range r.apps {
		if app.Status != AppStatusStartFailed && app.Status != AppStatusPullFailed {
			continue
		}
		retryCount := r.retryCount(name)
		backoff := min(time.Duration(1<<retryCount)*time.Minute, 10*time.Minute)
		lastAttempt := r.retryLastAttempt(name)
		if now.Sub(lastAttempt) >= backoff {
			slog.Info("Reconciler: retrying failed app", "app", name, "attempt", retryCount+1)
			r.setAppStatus(app, AppStatusPulling)
			r.recordRetryAttempt(name)
			toRestart = append(toRestart, name)
		}
	}
	r.mu.Unlock()

	for _, name := range toRestart {
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, name)
	}

	r.cleanupOldDiagnostics()
}

// checkLiveness runs liveness probes on running apps.
// Runs in both RUNNING and STALLED states so healthy apps continue to be monitored
// even when one app has failed (STALLED means at least one app died).
func (r *Runtime) checkLiveness(ctx context.Context) {
	r.mu.RLock()
	if r.status != NsStatusRunning && r.status != NsStatusStalled {
		r.mu.RUnlock()
		return
	}

	type livenessCheck struct {
		name        string
		containerID string
		probe       *appdef.AppProbeDef
		isCiteck    bool
	}
	var appsToCheck []livenessCheck

	for _, app := range r.apps {
		if app.Status == AppStatusRunning && app.ContainerID != "" && app.Def.LivenessProbe != nil {
			appsToCheck = append(appsToCheck, livenessCheck{
				name:        app.Name,
				containerID: app.ContainerID,
				probe:       app.Def.LivenessProbe,
				isCiteck:    app.Def.Kind.IsCiteckApp(),
			})
		}
	}
	r.mu.RUnlock()

	toRestart := make([]string, 0, len(appsToCheck))
	for _, check := range appsToCheck {
		alive := r.runLivenessProbe(ctx, check.containerID, check.probe)
		if alive {
			r.mu.Lock()
			delete(r.livenessFailures, check.name)
			r.mu.Unlock()
			continue
		}

		// Probe failed — increment failure counter
		threshold := check.probe.FailureThreshold
		if threshold <= 0 {
			threshold = 3
		}

		r.mu.Lock()
		r.livenessFailures[check.name]++
		failures := r.livenessFailures[check.name]

		if failures < threshold {
			slog.Warn("Liveness probe failed (below threshold)",
				"app", check.name, "failures", failures, "threshold", threshold)
			r.mu.Unlock()
			continue
		}

		// Threshold reached — verify app is still RUNNING before restarting
		app, ok := r.apps[check.name]
		if !ok || app.Status != AppStatusRunning {
			r.mu.Unlock()
			continue
		}
		containerID := app.ContainerID
		slog.Error("Liveness probe failed, restarting app",
			"app", check.name, "failures", failures, "threshold", threshold)
		r.mu.Unlock()

		// Capture diagnostics outside lock (may run Docker commands)
		reason := fmt.Sprintf("liveness probe failed %d/%d", failures, threshold)
		diag := r.captureDiagnostics(ctx, check.name, containerID, check.isCiteck, reason)

		r.mu.Lock()
		// Re-verify app still RUNNING after diagnostics (state may have changed)
		app, ok = r.apps[check.name]
		if !ok || app.Status != AppStatusRunning {
			r.mu.Unlock()
			continue
		}
		r.incrementRestartCount(check.name)
		r.recordRestartEvent(RestartEvent{
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			App:         check.name,
			Reason:      "liveness",
			Detail:      reason,
			Diagnostics: diag,
		})
		r.setAppStatus(app, AppStatusReadyToPull)
		delete(r.livenessFailures, check.name)
		r.persistState()
		r.mu.Unlock()

		toRestart = append(toRestart, check.name)
	}

	for _, name := range toRestart {
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, name)
	}
}

// runLivenessProbe executes a liveness probe against a container.
// HTTP probes use the container's network IP directly (no curl dependency).
func (r *Runtime) runLivenessProbe(ctx context.Context, containerID string, probe *appdef.AppProbeDef) bool {
	timeout := probe.TimeoutSeconds
	if timeout <= 0 {
		timeout = 5
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if probe.Exec != nil {
		_, exitCode, err := r.docker.ExecInContainer(probeCtx, containerID, probe.Exec.Command)
		return err == nil && exitCode == 0
	}
	if probe.HTTP != nil {
		host := r.docker.GetContainerIP(probeCtx, containerID)
		return httpProbeCheck(probeCtx, host, probe.HTTP.Port, probe.HTTP.Path, timeout)
	}
	return true
}

// captureDiagnostics captures thread dump and logs before restarting a container.
// Returns the path to the diagnostics file, or "" if capture fails.
func (r *Runtime) captureDiagnostics(ctx context.Context, appName, containerID string, isCiteck bool, reason string) string {
	diagCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf strings.Builder
	fmt.Fprintf(&buf, "=== RESTART DIAGNOSTICS ===\n")
	fmt.Fprintf(&buf, "App:       %s\n", appName)
	fmt.Fprintf(&buf, "Reason:    %s\n", reason)
	fmt.Fprintf(&buf, "Time:      %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&buf, "Container: %s\n\n", containerID)

	// Thread dump for Java apps
	if isCiteck {
		output, exitCode, err := r.docker.ExecInContainer(diagCtx, containerID, []string{"jcmd", "1", "Thread.print"})
		if err == nil && exitCode == 0 && output != "" {
			fmt.Fprintf(&buf, "=== THREAD DUMP ===\n%s\n\n", output)
		} else {
			fmt.Fprintf(&buf, "=== THREAD DUMP ===\n(jcmd failed: exit=%d err=%v)\n\n", exitCode, err)
		}
	}

	// Last 500 log lines
	logs, err := r.containerLogs(diagCtx, containerID, 500)
	if err == nil && logs != "" {
		fmt.Fprintf(&buf, "=== LAST 500 LOG LINES ===\n%s\n", logs)
	} else {
		fmt.Fprintf(&buf, "=== LAST 500 LOG LINES ===\n(failed: %v)\n", err)
	}

	// Save to file
	ts := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(r.volumesBase, "diagnostics", appName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		slog.Warn("Failed to create diagnostics dir", "err", err)
		return ""
	}
	path := filepath.Join(dir, ts+".txt")
	if err := fsutil.AtomicWriteFile(path, []byte(buf.String()), 0o644); err != nil {
		slog.Warn("Failed to write diagnostics", "err", err)
		return ""
	}

	slog.Info("Captured pre-restart diagnostics", "app", appName, "path", path)
	return path
}

// containerLogs fetches the last N lines from a container.
func (r *Runtime) containerLogs(ctx context.Context, containerID string, tail int) (string, error) {
	return r.docker.ContainerLogs(ctx, containerID, tail) //nolint:wrapcheck // thin wrapper
}

// cleanupOldDiagnostics removes diagnostics files older than 7 days.
func (r *Runtime) cleanupOldDiagnostics() {
	if r.volumesBase == "" {
		return
	}
	diagDir := filepath.Join(r.volumesBase, "diagnostics")
	entries, err := os.ReadDir(diagDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, appEntry := range entries {
		if !appEntry.IsDir() {
			continue
		}
		appDir := filepath.Join(diagDir, appEntry.Name())
		files, err := os.ReadDir(appDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(appDir, f.Name()))
			}
		}
	}
}

// gracefulShutdownOrder returns apps in the correct shutdown order (flat list).
// proxy -> webapps -> keycloak -> infrastructure (postgres, rabbitmq, zookeeper)
func gracefulShutdownOrder(apps []*AppRuntime) []*AppRuntime {
	var result []*AppRuntime
	for _, group := range GracefulShutdownGroups(apps) {
		result = append(result, group...)
	}
	return result
}

// GracefulShutdownGroups returns apps grouped for phased shutdown.
// Each group is stopped in parallel; groups are stopped sequentially.
// Order: [proxy] → [webapps+other] → [keycloak] → [infra]
func GracefulShutdownGroups(apps []*AppRuntime) [][]*AppRuntime {
	var proxy, webapps, keycloak, infra []*AppRuntime

	for _, app := range apps {
		switch app.Name {
		case appdef.AppProxy:
			proxy = append(proxy, app)
		case appdef.AppKeycloak:
			keycloak = append(keycloak, app)
		case appdef.AppPostgres, appdef.AppRabbitmq, appdef.AppZookeeper, appdef.AppMongodb:
			infra = append(infra, app)
		default:
			webapps = append(webapps, app)
		}
	}

	var groups [][]*AppRuntime
	if len(proxy) > 0 {
		groups = append(groups, proxy)
	}
	if len(webapps) > 0 {
		groups = append(groups, webapps)
	}
	if len(keycloak) > 0 {
		groups = append(groups, keycloak)
	}
	if len(infra) > 0 {
		groups = append(groups, infra)
	}
	return groups
}
