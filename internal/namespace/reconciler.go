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
	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// Liveness tolerance policy. The launcher recreates a RUNNING container only
// after livenessGraceSeconds of CONTINUOUS liveness failure — every generated
// LivenessProbe uses livenessFailureThreshold, and it is also the fallback for
// a probe that declares no threshold of its own.
//
// The window is (threshold-1) × period, not threshold × period: the counter
// starts on the FIRST failed probe, so N consecutive failures span N-1 periods.
// Hence the +1 below — with a 10s cadence, 7 failures ≈ 60s.
//
// 3 failures (≈20s) was too tight for the JVM apps this launcher runs. A full
// GC / safepoint pause does not refuse the connection, it just never answers,
// so every probe inside the pause burns its 5s timeout and counts as a failure:
// a deliberate diagnostic (measured 2026-08-12: a GC.heap_dump on citeck_eproc
// paused it past 3 failures in 16s) or an ordinary long pause under load got the
// container recreated mid-work. Restarting a busy-but-alive app is worse than
// carrying a genuinely dead one for another 40s, and the crash/OOM path in the
// reconciler — which does not wait on probes — still catches actual deaths.
//
// The period stays a per-app / daemon.yml knob (periodForProbe); an operator who
// lowers it deliberately shortens the window with it.
const (
	livenessGraceSeconds       = 60
	livenessProbePeriodSeconds = 10
	livenessFailureThreshold   = livenessGraceSeconds/livenessProbePeriodSeconds + 1
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
		// Matches periodForProbe's own fallback. They used to disagree (30s
		// here, 10s there) and this struct is only wired in when daemon.yml
		// actually carries a `reconciler:` section — so setting an unrelated
		// key like `reconciler.interval` silently tripled the probe period,
		// and with it the tolerance window derived from it.
		LivenessPeriod: livenessProbePeriodSeconds * time.Second,
	}
}

// testReconcileOnce runs one reconcile-diff cycle synchronously on the caller's
// goroutine. Used by tests; production code schedules ReconcileDiffTask from
// tickUnderLock.
//
// Mirrors what makeReconcileDiffPlan + handleReconcileDiffResult do together:
// snapshot RUNNING apps under RLock, run the diff/inspect outside any lock,
// then apply T18 under Lock.
func (r *Runtime) testReconcileOnce(ctx context.Context) {
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

// testLivenessCheckOnce runs one round of liveness probes synchronously on the
// caller's goroutine. Used by tests. Mirrors what the tick()-scheduled
// LivenessProbeTask + handleLivenessProbeResult do together, but runs probes
// serially rather than dispatching via the worker pool.
func (r *Runtime) testLivenessCheckOnce(ctx context.Context) {
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

// runLivenessProbe executes a liveness probe against a container. Mirrors the
// startup-probe target resolution in runtime_app.go so liveness uses the same
// reachable endpoint:
//   - HTTP probes prefer the published host port (127.0.0.1:hostPort) — the
//     daemon runs on the host and cannot reach Docker bridge IPs on most
//     Linux setups (`docker network` bridges are namespaced).
//   - When no host port is published, fall back to the container's bridge IP +
//     the container-side port. Works when the daemon happens to be on the same
//     bridge or when bridge routing is allowed.
//   - Exec probes go through `docker exec`, so addressing is non-issue.
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
		probeHost := ""
		probePort := r.docker.GetPublishedPort(probeCtx, containerID, probe.HTTP.Port)
		if probePort <= 0 {
			probeHost = r.docker.GetContainerIP(probeCtx, containerID)
			probePort = probe.HTTP.Port
		}
		if probePort <= 0 {
			return false
		}
		return httpProbeCheck(probeCtx, probeHost, probePort, probe.HTTP.Path, timeout)
	}
	return true
}

// Pre-restart diagnostics tuning.
const (
	diagLogTailLines = 500
	// A thread dump delivered through the container's stdout is 1500–2500
	// lines on a real webapp (132–225 threads), so the ordinary 500-line tail
	// would show its middle and nothing else — not the dump's start, not the
	// failure that preceded it.
	diagLogTailWithDump = 3000
	// The JVM is a child of the image's `sh -c /entrypoint.sh`, so the search
	// starts one generation down; 3 covers a wrapper script or two.
	diagJVMSearchDepth = 3
	// findJavaPIDScript locates the JVM inside the container. It matches on the
	// executable rather than on the command line: the scanning shell's OWN
	// cmdline contains the string "java" (it is in this script), so a cmdline
	// match reports the shell and the dump is then sent to the wrong process.
	findJavaPIDScript = `for p in /proc/[0-9]*; do case "$(readlink $p/exe 2>/dev/null)" in */java) echo ${p#/proc/}; break;; esac; done`
)

// diagSignalSettle is the time given to the JVM to finish writing a SIGQUIT
// dump to stdout before the log tail is read. A var so tests don't sleep.
var diagSignalSettle = 2 * time.Second

// captureThreadDump obtains a thread dump from a containerized JVM.
//
// It returns either the dump itself, or inLog=true meaning the JVM was asked to
// write the dump to its own stdout (the caller must then widen the log tail).
//
// Three mechanisms, because the images ship a JRE — there is no `jcmd`, `jmap`
// or `jstack` in `/opt/java/openjdk/bin`, and the previous implementation's
// `jcmd 1 Thread.print` was wrong twice over: the binary is absent AND pid 1 is
// the entrypoint shell, not the JVM. So, in order of how good the answer is:
//
//  1. The HotSpot attach protocol, spoken from the host (internal/jvmattach).
//     Needs nothing inside the container and keeps the dump out of the app's
//     log. Linux hosts only, and only when the daemon can signal the JVM's uid.
//  2. The same protocol spoken from INSIDE the container by the embedded
//     attach class, run by the image's own java (internal/appfiles). Also
//     returns the dump directly, and unlike (1) it works where the host has no
//     /proc entry for the JVM: macOS/Windows desktop, remote DOCKER_HOST.
//  3. SIGQUIT to the JVM inside the container. Works everywhere Docker does,
//     but the JVM answers into its own stdout, so the dump must be read back
//     out of the container log (inLog=true).
func (r *Runtime) captureThreadDump(ctx context.Context, containerID string) (dump string, inLog bool, err error) {
	if jvmattach.Supported {
		hostDump, attachErr := r.attachThreadDump(ctx, containerID)
		if attachErr == nil {
			return hostDump, false, nil
		}
		// Not an error yet — the paths below cover the common reasons
		// (uid mismatch, no /proc visibility, a JVM too wedged to answer).
		slog.Debug("Host attach thread dump failed, trying the in-container client",
			"container", containerID, "err", attachErr)
	}
	classDump, classErr := r.runAttachInContainer(ctx, containerID, attachThreadDumpCmd)
	if classErr == nil {
		return classDump, false, nil
	}
	slog.Debug("In-container attach thread dump failed, falling back to SIGQUIT",
		"container", containerID, "err", classErr)

	if sigErr := r.signalThreadDump(ctx, containerID); sigErr != nil {
		// Both failures are reported: whoever reads the diagnostics file later
		// cannot see the debug log, and "why did the class not work" is the
		// half that says whether the image or the JVM is the problem.
		return "", false, fmt.Errorf("in-container attach: %w; signal: %w", classErr, sigErr)
	}
	return "", true, nil
}

// attachThreadDump speaks the attach protocol to the JVM behind containerID,
// from this host. `threaddump` is HotSpot's own verb for it (see attachJcmd for
// the jcmd-spelled commands the operator can ask for).
func (r *Runtime) attachThreadDump(ctx context.Context, containerID string) (string, error) {
	return r.attachHostCommand(ctx, containerID, attachThreadDumpCmd)
}

// signalThreadDump asks the JVM to dump its threads to stdout, from inside the
// container. The dump lands in the container log, which captureDiagnostics
// reads right after.
func (r *Runtime) signalThreadDump(ctx context.Context, containerID string) error {
	out, code, err := r.docker.ExecInContainer(ctx, containerID, []string{"sh", "-c", findJavaPIDScript})
	if err != nil {
		return fmt.Errorf("locate jvm in container: %w", err)
	}
	pid := strings.TrimSpace(out)
	if code != 0 || pid == "" {
		return fmt.Errorf("locate jvm in container: exit=%d out=%q", code, truncateForLog(out))
	}
	// `kill` is a shell builtin — the images have no /bin/kill, no pgrep, no ps.
	if _, code, err := r.docker.ExecInContainer(ctx, containerID, []string{"sh", "-c", "kill -3 " + pid}); err != nil || code != 0 {
		return fmt.Errorf("send SIGQUIT to pid %s: exit=%d err=%w", pid, code, err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for SIGQUIT dump: %w", ctx.Err())
	case <-time.After(diagSignalSettle):
	}
	return nil
}

func truncateForLog(s string) string {
	const maxLen = 120
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
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
	logTail := diagLogTailLines
	if isCiteck {
		dump, inLog, err := r.captureThreadDump(diagCtx, containerID)
		switch {
		case err != nil:
			fmt.Fprintf(&buf, "=== THREAD DUMP ===\n(unavailable: %v)\n\n", err)
		case inLog:
			// The JVM wrote it to its own stdout, so it is already on its way
			// into the log section below — which must then be long enough to
			// hold both the dump and the failure that preceded it.
			logTail = diagLogTailWithDump
			fmt.Fprintf(&buf, "=== THREAD DUMP ===\n(sent SIGQUIT — the dump is in the log section below)\n\n")
		default:
			fmt.Fprintf(&buf, "=== THREAD DUMP ===\n%s\n\n", dump)
		}
	}

	logs, err := r.containerLogs(diagCtx, containerID, logTail)
	if err == nil && logs != "" {
		fmt.Fprintf(&buf, "=== LAST %d LOG LINES ===\n%s\n", logTail, logs)
	} else {
		fmt.Fprintf(&buf, "=== LAST %d LOG LINES ===\n(failed: %v)\n", logTail, err)
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
