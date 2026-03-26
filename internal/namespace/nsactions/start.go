package nsactions

import (
	"context"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/actions"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/docker"
)

const (
	// ContainerCreateRetries matches Kotlin AppStartAction (3 retries).
	ContainerCreateRetries = 3
	// ContainerCreateRetryDelay between create attempts.
	ContainerCreateRetryDelay = 2 * time.Second
)

// StartData carries the container start parameters.
type StartData struct {
	AppName     string
	AppDef      appdef.ApplicationDef
	VolumesBase string
	// ContainerID is set by the executor on success.
	ContainerID string
}

// StartExecutor creates and starts a container with retry on conflict.
type StartExecutor struct {
	Docker *docker.Client
}

func (e *StartExecutor) Execute(ctx context.Context, actx *actions.ActionContext) error {
	d := actx.Data.(*StartData)
	containerName := e.Docker.ContainerName(d.AppName)

	// Remove existing container to avoid conflicts
	_ = e.Docker.StopAndRemoveContainer(ctx, containerName)

	id, err := e.Docker.CreateContainer(ctx, d.AppDef, d.VolumesBase)
	if err != nil {
		return fmt.Errorf("create container %s: %w", d.AppName, err)
	}

	if err := e.Docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("start container %s: %w", d.AppName, err)
	}

	d.ContainerID = id
	return nil
}

func (e *StartExecutor) Name(actx *actions.ActionContext) string {
	d := actx.Data.(*StartData)
	return fmt.Sprintf("Start %s", d.AppName)
}

func (e *StartExecutor) RetryDelay(actx *actions.ActionContext) time.Duration {
	if actx.Attempt >= ContainerCreateRetries {
		return -1
	}
	return ContainerCreateRetryDelay
}
