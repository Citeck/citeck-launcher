package docker

import (
	"context"
	"io"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/docker/docker/api/types"
)

// RuntimeClient defines the Docker operations required by the namespace runtime
// (internal/namespace). This is a subset of *Client — the daemon HTTP handlers
// use *Client directly because they need additional methods (NetworkName, Ping,
// Close, etc.) that are HTTP-handler-specific and not part of the runtime's concern.
type RuntimeClient interface {
	ContainerName(appName string) string
	CreateNetwork(ctx context.Context) (string, error)
	RemoveNetwork(ctx context.Context) error
	CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeoutSec int) error
	RemoveContainer(ctx context.Context, id string) error
	StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error
	GetContainers(ctx context.Context) ([]types.Container, error)
	InspectContainer(ctx context.Context, id string) (types.ContainerJSON, error)
	PullImage(ctx context.Context, img string, auth *RegistryAuth) error
	PullImageWithProgress(ctx context.Context, img string, auth *RegistryAuth, progressFn PullProgressFn) error
	ImageExists(ctx context.Context, img string) bool
	GetImageDigest(ctx context.Context, img string) string
	ContainerLogs(ctx context.Context, containerID string, tail int) (string, error)
	ContainerLogsFollow(ctx context.Context, containerID string, tail int) (io.ReadCloser, error)
	ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, int, error)
	GetPublishedPort(ctx context.Context, containerID string, containerPort int) int
	GetContainerIP(ctx context.Context, containerID string) string
	ContainerStats(ctx context.Context, containerID string) (*ContainerStat, error)
	WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error
	WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error
}

// Verify *Client implements RuntimeClient at compile time.
var _ RuntimeClient = (*Client)(nil)
