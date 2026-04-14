package nsactions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// PullRetryDelays defines standard pull retry delays matching Kotlin RETRY_DELAYS: [1s, 1s, 1s, 5s, 10s].
var PullRetryDelays = []time.Duration{
	time.Second, time.Second, time.Second, 5 * time.Second, 10 * time.Second,
}

// InitPullRetryDelays defines shorter delays for init container images.
var InitPullRetryDelays = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second,
}

// PullRetriesForExistingImage is the threshold after which a local image is used on pull failure.
const PullRetriesForExistingImage = 3

// PullData carries the image pull parameters.
type PullData struct {
	AppName    string
	Image      string
	Auth       *docker.RegistryAuth  // optional registry credentials
	ProgressFn docker.PullProgressFn // optional progress callback
}

// PullExecutor pulls a Docker image with configurable retry delays and fallback to local.
type PullExecutor struct {
	Docker      docker.RuntimeClient
	RetryDelays []time.Duration // if nil, uses PullRetryDelays
	PullAlways  bool            // if true, pull even if image exists locally (for updates)
}

func (e *PullExecutor) retryDelays() []time.Duration {
	if e.RetryDelays != nil {
		return e.RetryDelays
	}
	return PullRetryDelays
}

// Execute pulls the Docker image with retry and fallback logic.
func (e *PullExecutor) Execute(ctx context.Context, actx *actions.ActionContext) error {
	d := actx.Data.(*PullData)

	// Skip pull only if image exists and PullAlways is not set
	if !e.PullAlways && e.Docker.ImageExists(ctx, d.Image) {
		return nil
	}

	// Wrap progress callback to send heartbeat on every progress update,
	// preventing stall detection from canceling long pulls (e.g. 4GB images).
	progressFn := d.ProgressFn
	wrappedProgress := func(currentMB, totalMB float64, pct int) {
		actx.Heartbeat()
		if progressFn != nil {
			progressFn(currentMB, totalMB, pct)
		}
	}

	err := e.Docker.PullImageWithProgress(ctx, d.Image, d.Auth, wrappedProgress)
	if err == nil {
		return nil
	}

	// After N failures, use local image if it exists
	if actx.Attempt >= PullRetriesForExistingImage && e.Docker.ImageExists(ctx, d.Image) {
		return nil
	}

	// Detect 401/403 and provide actionable message with registry host
	errStr := err.Error()
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") || strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "denied") {
		host := registryHost(d.Image)
		return fmt.Errorf("pull %s: authentication failed — run: docker login %s", d.Image, host)
	}
	return fmt.Errorf("pull %s: %w", d.Image, err)
}

// Name returns a human-readable description of the pull action.
func (e *PullExecutor) Name(actx *actions.ActionContext) string {
	d := actx.Data.(*PullData)
	if actx.Attempt > 0 {
		return fmt.Sprintf("Pull %s (%d)", d.AppName, actx.Attempt+1)
	}
	return fmt.Sprintf("Pull %s", d.AppName)
}

// RetryDelay returns the delay before the next pull retry attempt.
func (e *PullExecutor) RetryDelay(actx *actions.ActionContext) time.Duration {
	delays := e.retryDelays()
	if actx.Attempt >= len(delays) {
		return -1 // stop retrying
	}
	return delays[actx.Attempt]
}

// registryHost extracts the registry host from a Docker image reference.
func registryHost(img string) string {
	// "nexus.citeck.ru/ecos-model:1.0" → "nexus.citeck.ru"
	// "docker.io/library/postgres:17" → "docker.io"
	if idx := strings.IndexByte(img, '/'); idx > 0 && strings.ContainsAny(img[:idx], ".:") {
		return img[:idx]
	}
	return "docker.io"
}
