package namespace

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/niceteck/citeck-launcher/internal/appdef"
	"github.com/niceteck/citeck-launcher/internal/docker"
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

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
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
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
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

	// Get actual containers
	containers, err := r.docker.GetContainers(ctx)
	if err != nil {
		slog.Warn("Reconciler: failed to list containers", "err", err)
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

	r.mu.Lock()
	for name, app := range r.apps {
		if app.Status == AppStatusRunning && !containersByApp[name] {
			slog.Warn("Reconciler: container missing, will restart", "app", name)
			r.setAppStatus(app, AppStatusReadyToPull)
			toRestart = append(toRestart, name)
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

// GracefulShutdownOrder returns apps in the correct shutdown order.
// proxy -> webapps -> keycloak -> infrastructure (postgres, rabbitmq, zookeeper)
func GracefulShutdownOrder(apps []*AppRuntime) []*AppRuntime {
	var proxy, webapps, keycloak, infra, other []*AppRuntime

	for _, app := range apps {
		switch app.Name {
		case appdef.AppProxy:
			proxy = append(proxy, app)
		case appdef.AppKeycloak:
			keycloak = append(keycloak, app)
		case appdef.AppPostgres, appdef.AppRabbitmq, appdef.AppZookeeper, appdef.AppMongodb:
			infra = append(infra, app)
		default:
			if app.Def.Kind.IsCiteckApp() {
				webapps = append(webapps, app)
			} else {
				other = append(other, app)
			}
		}
	}

	var result []*AppRuntime
	result = append(result, proxy...)
	result = append(result, webapps...)
	result = append(result, other...)
	result = append(result, keycloak...)
	result = append(result, infra...)
	return result
}
