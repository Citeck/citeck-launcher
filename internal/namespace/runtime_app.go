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

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/docker/docker/pkg/stdcopy"
)

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
