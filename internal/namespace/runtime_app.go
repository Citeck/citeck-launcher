package namespace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace/nsactions"
	"github.com/docker/docker/pkg/stdcopy"
)

func (r *Runtime) pullAndStartApp(ctx context.Context, appName string) {
	defer r.appWg.Done()

	r.mu.RLock()
	app := r.apps[appName]
	if app == nil {
		r.mu.RUnlock()
		return
	}
	appDef := app.Def
	r.mu.RUnlock()

	// Pull image via action service — pull policy:
	// THIRD_PARTY: never re-pull (stable images)
	// Other apps: only re-pull if image tag contains "snapshot" (case-insensitive)
	if appDef.Image != "" { //nolint:nestif // pull + semaphore + progress nesting is inherent
		// Pull image under semaphore (max concurrent pulls)
		pullErr := func() error {
			select {
			case r.pullSem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-r.pullSem }()

			pullAlways := shouldPullImage(appDef.Kind, appDef.Image)
			var auth *docker.RegistryAuth
			if r.registryAuthFn != nil {
				auth = r.registryAuthFn(appDef.Image)
			}
			var lastProgressReport time.Time
			progressFn := func(_, totalMB float64, pct int) {
				now := time.Now()
				if now.Sub(lastProgressReport) < time.Second {
					return
				}
				lastProgressReport = now
				r.mu.Lock()
				app.StatusText = fmt.Sprintf("Pulling: %.0fmb %d%%", totalMB, pct)
				r.mu.Unlock()
			}
			pullHandle := r.actionSvc.Execute(actions.ActionParams{
				Executor: &nsactions.PullExecutor{Docker: r.docker, PullAlways: pullAlways},
				Data:     &nsactions.PullData{AppName: appName, Image: appDef.Image, Auth: auth, ProgressFn: progressFn},
			})
			return pullHandle.Wait(ctx)
		}()

		if pullErr != nil {
			if ctx.Err() != nil {
				return // canceled by shutdown — not a failure
			}
			r.mu.Lock()
			r.setAppStatus(app, AppStatusPullFailed)
			app.StatusText = pullErr.Error()
			r.mu.Unlock()
			return
		}

		// Clear pull status text and fetch image digest for deployment hash
		r.mu.Lock()
		app.StatusText = ""
		r.mu.Unlock()
		if digest := r.docker.GetImageDigest(ctx, appDef.Image); digest != "" {
			r.mu.Lock()
			appDef.ImageDigest = digest
			app.Def = appDef
			r.mu.Unlock()
		}
	}

	// Wait for dependencies
	if !r.waitForDeps(ctx, appName) {
		return // context canceled (shutdown)
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusStarting)
	r.mu.Unlock()

	// Run init containers
	if err := r.runInitContainers(ctx, appName, appDef); err != nil {
		slog.Error("Init container failed", "app", appName, "err", err)
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = fmt.Sprintf("init container: %v", err)
		r.mu.Unlock()
		return
	}

	// Create and start container via action service
	startData := &nsactions.StartData{
		AppName: appName, AppDef: appDef, VolumesBase: r.volumesBase,
	}
	startHandle := r.actionSvc.Execute(actions.ActionParams{
		Executor: &nsactions.StartExecutor{Docker: r.docker},
		Data:     startData,
	})
	if err := startHandle.Wait(ctx); err != nil {
		r.mu.Lock()
		r.setAppStatus(app, AppStatusStartFailed)
		app.StatusText = err.Error()
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	app.ContainerID = startData.ContainerID
	r.mu.Unlock()

	// Wait for startup probe
	if len(appDef.StartupConditions) > 0 {
		if err := r.waitForStartup(ctx, appName, startData.ContainerID, appDef.StartupConditions); err != nil {
			slog.Error("Startup probe failed", "app", appName, "err", err)
			r.mu.Lock()
			r.setAppStatus(app, AppStatusStartFailed)
			app.StatusText = err.Error()
			r.mu.Unlock()
			return
		}
	}

	// Run init actions (after startup probe — e.g. postgres DB creation)
	for _, action := range appDef.InitActions {
		if len(action.Exec) > 0 {
			slog.Info("Running init action", "app", appName, "cmd", action.Exec)
			output, exitCode, execErr := r.docker.ExecInContainer(ctx, startData.ContainerID, action.Exec)
			if execErr != nil {
				slog.Warn("Init action exec error", "app", appName, "cmd", action.Exec, "err", execErr)
			} else if exitCode != 0 {
				slog.Warn("Init action exited with non-zero code", "app", appName, "cmd", action.Exec, "exitCode", exitCode, "output", output)
			}
		}
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusRunning)
	r.mu.Unlock()
}

func (r *Runtime) waitForDeps(ctx context.Context, appName string) bool {
	r.mu.RLock()
	app := r.apps[appName]
	deps := app.Def.DependsOn
	r.mu.RUnlock()

	if len(deps) == 0 {
		return true
	}

	r.mu.Lock()
	r.setAppStatus(app, AppStatusDepsWaiting)
	r.mu.Unlock()

	for {
		r.mu.RLock()
		allReady := true
		for dep := range deps {
			depApp, ok := r.apps[dep]
			if !ok {
				// Dep is not part of the current generation — treated as
				// satisfied so dependents don't stall. This lets generators
				// declare a stable dependency set across configuration modes
				// (e.g., webapps always list keycloak even in BASIC auth
				// mode where the keycloak container is intentionally absent),
				// keeping deployment hashes stable.
				continue
			}
			// Detached apps (manually stopped) are considered satisfied —
			// the user intentionally disabled them, don't block dependents.
			if depApp.Status == AppStatusRunning || r.manualStoppedApps[dep] {
				continue
			}
			allReady = false
			break
		}
		// Capture current notify channel under the same lock to avoid races
		notify := r.statusNotify
		r.mu.RUnlock()

		if allReady {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-notify:
			// Status changed, re-check deps
		}
	}
}

func (r *Runtime) runInitContainers(ctx context.Context, appName string, appDef appdef.ApplicationDef) error {
	for _, initC := range appDef.InitContainers {
		slog.Info("Running init container", "app", appName, "image", initC.Image)
		initDef := appdef.ApplicationDef{
			Name: appName + "-init", Image: initC.Image,
			Cmd: initC.Cmd, Volumes: initC.Volumes, Environments: initC.Environments,
			Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "100m"}},
			IsInit:    true, // no restart policy for init containers
		}

		// Pull init image via action service
		var initAuth *docker.RegistryAuth
		if r.registryAuthFn != nil {
			initAuth = r.registryAuthFn(initC.Image)
		}
		pullHandle := r.actionSvc.Execute(actions.ActionParams{
			Executor: &nsactions.PullExecutor{Docker: r.docker, RetryDelays: nsactions.InitPullRetryDelays},
			Data:     &nsactions.PullData{AppName: appName, Image: initC.Image, Auth: initAuth},
		})
		if err := pullHandle.Wait(ctx); err != nil {
			return fmt.Errorf("pull init image %s: %w", initC.Image, err)
		}

		initName := r.docker.ContainerName(appName + "-init")
		_ = r.docker.StopAndRemoveContainer(ctx, initName, 0)
		initID, err := r.docker.CreateContainer(ctx, initDef, r.volumesBase)
		if err != nil {
			return fmt.Errorf("create init container for %s: %w", appName, err)
		}
		if err := r.docker.StartContainer(ctx, initID); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.docker.RemoveContainer(cleanupCtx, initID)
			cleanupCancel()
			return fmt.Errorf("start init container for %s: %w", appName, err)
		}
		// Wait for init container to EXIT (not start)
		if err := r.docker.WaitForContainerExit(ctx, initID, 60*time.Second); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.docker.RemoveContainer(cleanupCtx, initID)
			cleanupCancel()
			return fmt.Errorf("init container exited with error for %s: %w", appName, err)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.docker.RemoveContainer(cleanupCtx, initID)
		cleanupCancel()
	}
	return nil
}

func (r *Runtime) waitForStartup(ctx context.Context, _, containerID string, conditions []appdef.StartupCondition) error {
	for _, cond := range conditions {
		if cond.Log != nil {
			if err := r.waitForLogPattern(ctx, containerID, cond.Log); err != nil {
				return err
			}
		}
		if cond.Probe != nil {
			if err := r.waitForProbe(ctx, containerID, cond.Probe); err != nil {
				return err
			}
		}
	}
	return nil
}

// waitForLogPattern watches Docker container logs for a regex pattern match using follow streaming.
func (r *Runtime) waitForLogPattern(ctx context.Context, containerID string, cond *appdef.LogStartupCondition) error {
	timeout := time.Duration(cond.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	pattern, err := regexp.Compile(cond.Pattern)
	if err != nil {
		return fmt.Errorf("invalid log pattern %q: %w", cond.Pattern, err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shortID := truncateID(containerID)

	// Use Docker follow to stream logs, demux through stdcopy to strip Docker multiplex headers
	rawReader, err := r.docker.ContainerLogsFollow(timeoutCtx, containerID, 50)
	if err != nil {
		return fmt.Errorf("follow logs %s: %w", shortID, err)
	}
	defer rawReader.Close()

	// Pipe demuxed output for clean line scanning
	pr, pw := io.Pipe()
	defer pr.Close() // unblocks stdcopy goroutine on early return
	go func() {
		_, _ = stdcopy.StdCopy(pw, pw, rawReader)
		_ = pw.Close()
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if pattern.MatchString(line) {
			slog.Debug("Log pattern matched", "container", shortID, "pattern", cond.Pattern)
			return nil
		}
	}

	if ctx.Err() != nil {
		return fmt.Errorf("wait for log pattern %q in %s: %w", cond.Pattern, shortID, ctx.Err())
	}
	return fmt.Errorf("log pattern %q not found in %s after %v", cond.Pattern, shortID, timeout)
}

func (r *Runtime) waitForProbe(ctx context.Context, containerID string, probe *appdef.AppProbeDef) error {
	delay := probe.InitialDelaySeconds
	if delay <= 0 {
		delay = 5
	}
	period := probe.PeriodSeconds
	if period <= 0 {
		period = 10
	}
	threshold := probe.FailureThreshold
	if threshold <= 0 {
		threshold = 360 // ~1 hour with default 10s period
	}

	// Context-aware initial delay
	select {
	case <-ctx.Done():
		return fmt.Errorf("probe initial delay: %w", ctx.Err())
	case <-time.After(time.Duration(delay) * time.Second):
	}

	shortID := truncateID(containerID)

	for attempt := 0; attempt < threshold; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("probe attempt %d: %w", attempt, ctx.Err())
		default:
		}

		if probe.Exec != nil {
			_, exitCode, err := r.docker.ExecInContainer(ctx, containerID, probe.Exec.Command)
			if err == nil && exitCode == 0 {
				slog.Info("Exec probe passed", "container", shortID, "attempt", attempt)
				return nil
			}
		}
		if probe.HTTP != nil {
			// Try published port first (localhost), fall back to container IP (Docker network)
			probeHost := ""
			probePort := r.docker.GetPublishedPort(ctx, containerID, probe.HTTP.Port)
			if probePort <= 0 {
				probeHost = r.docker.GetContainerIP(ctx, containerID)
				probePort = probe.HTTP.Port
			}
			if attempt == 0 || attempt%10 == 0 {
				slog.Info("HTTP probe", "container", shortID,
					"containerPort", probe.HTTP.Port, "probeHost", probeHost, "probePort", probePort,
					"path", probe.HTTP.Path, "attempt", attempt)
			}
			if probePort > 0 && httpProbeCheck(ctx, probeHost, probePort, probe.HTTP.Path, probe.TimeoutSeconds) {
				slog.Info("HTTP probe passed", "container", shortID, "port", probePort, "attempt", attempt)
				return nil
			}
		}
		// Context-aware period sleep
		select {
		case <-ctx.Done():
			return fmt.Errorf("probe period sleep: %w", ctx.Err())
		case <-time.After(time.Duration(period) * time.Second):
		}
	}

	return fmt.Errorf("probe failed after %d attempts", threshold)
}

// shouldPullImage determines if an image should be re-pulled based on app kind and image tag.
// THIRD_PARTY apps: never re-pull. Others: only re-pull if tag contains "snapshot".
func shouldPullImage(kind appdef.ApplicationKind, img string) bool {
	if kind == appdef.KindThirdParty {
		return false
	}
	lower := strings.ToLower(img)
	return strings.Contains(lower, "snapshot")
}

type existingContainer struct {
	containerID string
	hash        string
	running     bool
}

// buildExistingContainerMap gets current Docker containers and their deployment hashes.
func (r *Runtime) buildExistingContainerMap(ctx context.Context) map[string]existingContainer {
	containers, err := r.docker.GetContainers(ctx)
	if err != nil {
		return nil
	}
	result := make(map[string]existingContainer)
	for _, c := range containers {
		appName := c.Labels[docker.LabelAppName]
		if appName == "" {
			continue
		}
		result[appName] = existingContainer{
			containerID: c.ID,
			hash:        c.Labels[docker.LabelAppHash],
			running:     c.State == "running",
		}
	}
	return result
}
