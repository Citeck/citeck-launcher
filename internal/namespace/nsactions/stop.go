package nsactions

import (
	"context"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// StopData carries the container stop parameters.
type StopData struct {
	AppName       string
	ContainerName string
}

// StopExecutor stops and removes a container with 1s retry.
type StopExecutor struct {
	Docker docker.RuntimeClient
}

// Execute stops and removes the container.
func (e *StopExecutor) Execute(ctx context.Context, actx *actions.ActionContext) error {
	d := actx.Data.(*StopData)
	if err := e.Docker.StopAndRemoveContainer(ctx, d.ContainerName, 0); err != nil {
		return fmt.Errorf("stop %s: %w", d.AppName, err)
	}
	return nil
}

// Name returns a human-readable description of the stop action.
func (e *StopExecutor) Name(actx *actions.ActionContext) string {
	d := actx.Data.(*StopData)
	return fmt.Sprintf("Stop %s", d.AppName)
}

// RetryDelay returns the delay before the next stop retry.
func (e *StopExecutor) RetryDelay(actx *actions.ActionContext) time.Duration {
	if actx.Attempt >= 2 {
		return -1
	}
	return time.Second
}
