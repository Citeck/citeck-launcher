package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/docker/docker/client"
)

// ReconcilerConfig holds reconciliation settings.
type ReconcilerConfig struct {
	Enabled         bool
	IntervalSeconds int
	LivenessEnabled bool
	LivenessPeriod  time.Duration
}

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

	r.reconcileWg.Add(1)
	go func() {
		defer r.reconcileWg.Done()
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
	}()

	if cfg.LivenessEnabled {
		r.reconcileWg.Add(1)
		go func() {
			defer r.reconcileWg.Done()
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
		}()
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

	// Collect apps needing restart under lock, then spawn goroutines after unlock
	var toRestart []string
	now := time.Now()

	r.mu.Lock()
	for name, app := range r.apps {
		if app.Status == AppStatusRunning && !containersByApp[name] {
			// Check if OOM killed
			if app.ContainerID != "" {
				inspCtx, inspCancel := context.WithTimeout(ctx, 5*time.Second)
				info, inspErr := r.docker.InspectContainer(inspCtx, app.ContainerID)
				inspCancel()
				if inspErr == nil && info.State != nil && info.State.OOMKilled {
					slog.Warn("Reconciler: container OOM killed, will restart", "app", name)
					r.emitEvent(api.EventDto{
						Type: "app_oom", Timestamp: time.Now().UnixMilli(),
						NamespaceID: r.nsID, AppName: name, After: "OOMKilled",
					})
				} else {
					slog.Warn("Reconciler: container missing, will restart", "app", name)
				}
			} else {
				slog.Warn("Reconciler: container missing, will restart", "app", name)
			}
			r.setAppStatus(app, AppStatusReadyToPull)
			toRestart = append(toRestart, name)
		}
		// Retry failed apps with exponential backoff (1m, 2m, 4m, ..., max 30m)
		if app.Status == AppStatusStartFailed || app.Status == AppStatusPullFailed {
			retryCount := r.retryCount(name)
			backoff := time.Duration(1<<retryCount) * time.Minute
			if backoff > 30*time.Minute {
				backoff = 30 * time.Minute
			}
			lastAttempt := r.retryLastAttempt(name)
			if now.Sub(lastAttempt) >= backoff {
				slog.Info("Reconciler: retrying failed app", "app", name, "attempt", retryCount+1)
				r.setAppStatus(app, AppStatusPulling)
				r.recordRetryAttempt(name)
				toRestart = append(toRestart, name)
			}
		}
	}
	r.mu.Unlock()

	for _, name := range toRestart {
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, name)
	}
}

// checkLiveness runs liveness probes on running apps.
func (r *Runtime) checkLiveness(ctx context.Context) {
	r.mu.RLock()
	if r.status != NsStatusRunning {
		r.mu.RUnlock()
		return
	}

	var appsToCheck []struct {
		name        string
		containerID string
		probe       *appdef.AppProbeDef
	}

	for _, app := range r.apps {
		if app.Status == AppStatusRunning && app.ContainerID != "" && app.Def.LivenessProbe != nil {
			appsToCheck = append(appsToCheck, struct {
				name        string
				containerID string
				probe       *appdef.AppProbeDef
			}{app.Name, app.ContainerID, app.Def.LivenessProbe})
		}
	}
	r.mu.RUnlock()

	var toRestart []string
	for _, check := range appsToCheck {
		alive := r.runLivenessProbe(ctx, check.containerID, check.probe)
		if !alive {
			slog.Warn("Liveness probe failed, will restart", "app", check.name)
			r.mu.Lock()
			if app, ok := r.apps[check.name]; ok && app.Status == AppStatusRunning {
				r.setAppStatus(app, AppStatusReadyToPull)
				toRestart = append(toRestart, check.name)
			}
			r.mu.Unlock()
		}
	}
	for _, name := range toRestart {
		r.appWg.Add(1)
		go r.pullAndStartApp(ctx, name)
	}
}

func (r *Runtime) runLivenessProbe(ctx context.Context, containerID string, probe *appdef.AppProbeDef) bool {
	if probe.Exec != nil {
		_, exitCode, err := r.docker.ExecInContainer(ctx, containerID, probe.Exec.Command)
		return err == nil && exitCode == 0
	}
	if probe.HTTP != nil {
		cmd := []string{"sh", "-c", fmt.Sprintf("curl -sf -o /dev/null http://localhost:%d%s", probe.HTTP.Port, probe.HTTP.Path)}
		_, exitCode, err := r.docker.ExecInContainer(ctx, containerID, cmd)
		return err == nil && exitCode == 0
	}
	return true
}

// GracefulShutdownOrder returns apps in the correct shutdown order (flat list).
// proxy -> webapps -> keycloak -> infrastructure (postgres, rabbitmq, zookeeper)
func GracefulShutdownOrder(apps []*AppRuntime) []*AppRuntime {
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
