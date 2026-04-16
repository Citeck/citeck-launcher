package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// ReconcilerConfig holds reconciliation settings from daemon.yml.
// SetReconcilerConfig wires IntervalSeconds / LivenessPeriod into the
// runtime's reconcilerInterval / liveness defaults.
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

// reconcileOnce runs one reconcile-diff cycle synchronously on the caller's
// goroutine. Used by tests; production code schedules ReconcileDiffTask from
// tickUnderLock.
//
// Mirrors what makeReconcileDiffPlan + handleReconcileDiffResult do together:
// snapshot RUNNING apps under RLock, run the diff/inspect outside any lock,
// then apply T18 under Lock.
func (r *Runtime) reconcileOnce(ctx context.Context) {
	r.mu.RLock()
	if r.status != NsStatusRunning && r.status != NsStatusStalled {
		r.mu.RUnlock()
		return
	}
	snapshot := make([]reconcileSnapshotEntry, 0, len(r.apps))
	for name, app := range r.apps {
		if app.Status != AppStatusRunning {
			continue
		}
		snapshot = append(snapshot, reconcileSnapshotEntry{Name: name, ContainerID: app.ContainerID})
	}
	r.mu.RUnlock()

	// Run the worker body inline (no dispatcher roundtrip) and apply the
	// Result via the same handler runtimeLoop uses. Stamp the TaskID/AttemptID
	// so applyWorkerResult's staleness guard does not drop it.
	res := r.runReconcileDiffTask(ctx, snapshot)
	res.TaskID = workers.TaskID{App: "", Op: workers.OpReconcileDiff}
	r.handleReconcileDiffResult(res)
}

// livenessCheckOnce runs one round of liveness probes synchronously on the
// caller's goroutine. Used by tests. Mirrors what the tick()-scheduled
// LivenessProbeTask + handleLivenessProbeResult do together, but runs probes
// serially rather than dispatching via the worker pool.
func (r *Runtime) livenessCheckOnce(ctx context.Context) {
	type check struct {
		name        string
		containerID string
		probe       *appdef.AppProbeDef
	}
	r.mu.RLock()
	if r.status != NsStatusRunning && r.status != NsStatusStalled {
		r.mu.RUnlock()
		return
	}
	var checks []check
	for _, app := range r.apps {
		if app.Status == AppStatusRunning && app.ContainerID != "" && app.Def.LivenessProbe != nil {
			checks = append(checks, check{
				name:        app.Name,
				containerID: app.ContainerID,
				probe:       app.Def.LivenessProbe,
			})
		}
	}
	r.mu.RUnlock()

	for _, c := range checks {
		healthy := r.runLivenessProbe(ctx, c.containerID, c.probe)
		res := workers.Result{
			TaskID:  workers.TaskID{App: c.name, Op: workers.OpLivenessProbe},
			Payload: workers.LivenessProbePayload{Healthy: healthy},
		}
		r.handleLivenessProbeResult(res)
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
