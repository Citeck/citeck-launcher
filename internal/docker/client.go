package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/niceteck/citeck-launcher/internal/appdef"
)

// Labels used to track Citeck containers.
const (
	LabelWorkspace = "citeck.launcher.workspace"
	LabelNamespace = "citeck.launcher.namespace"
	LabelAppName   = "citeck.launcher.app"
)

// Client wraps the Docker SDK client with Citeck-specific operations.
type Client struct {
	cli       *client.Client
	workspace string
	namespace string
}

// NewClient creates a Docker client.
// It auto-detects the Docker socket: DOCKER_HOST env, rootless, or standard.
func NewClient(workspace, namespace string) (*Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}

	// If DOCKER_HOST is not set, try common socket locations
	if os.Getenv("DOCKER_HOST") == "" {
		socketPath := detectDockerSocket()
		if socketPath != "" {
			opts = append(opts, client.WithHost("unix://"+socketPath))
		}
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{cli: cli, workspace: workspace, namespace: namespace}, nil
}

// detectDockerSocket finds the Docker socket in common locations.
func detectDockerSocket() string {
	candidates := []string{
		"/var/run/docker.sock",
		fmt.Sprintf("/run/user/%d/docker.sock", os.Getuid()),
		os.Getenv("HOME") + "/.docker/desktop/docker.sock",
	}
	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return path
		}
	}
	return ""
}

func (c *Client) Close() error {
	return c.cli.Close()
}

// Ping checks Docker connectivity.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// ContainerName generates the Docker container name.
func (c *Client) ContainerName(appName string) string {
	return fmt.Sprintf("citeck_%s_%s_%s", appName, c.namespace, c.workspace)
}

// NetworkName returns the Docker network name for this namespace.
func (c *Client) NetworkName() string {
	return fmt.Sprintf("citeck_network_%s_%s", c.namespace, c.workspace)
}

// CreateNetwork creates a bridge network for the namespace.
func (c *Client) CreateNetwork(ctx context.Context) (string, error) {
	name := c.NetworkName()

	// Check if exists
	networks, err := c.cli.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("name", name)),
	})
	if err != nil {
		return "", err
	}
	if len(networks) > 0 {
		return networks[0].ID, nil
	}

	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// RemoveNetwork removes the namespace network.
func (c *Client) RemoveNetwork(ctx context.Context) error {
	return c.cli.NetworkRemove(ctx, c.NetworkName())
}

// CreateContainer creates a container from an ApplicationDef.
func (c *Client) CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error) {
	containerName := c.ContainerName(app.Name)
	networkName := c.NetworkName()

	// Environment
	env := make([]string, 0, len(app.Environments))
	for k, v := range app.Environments {
		env = append(env, k+"="+v)
	}

	// Port bindings
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, p := range app.Ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPort := parts[0]
		containerPort := nat.Port(parts[1] + "/tcp")
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []nat.PortBinding{{HostPort: hostPort}}
	}

	// Volumes (binds)
	var binds []string
	for _, v := range app.Volumes {
		// Resolve relative paths
		if strings.HasPrefix(v, "./") && volumesBaseDir != "" {
			v = volumesBaseDir + "/" + v[2:]
		}
		binds = append(binds, v)
	}

	// Labels
	labels := map[string]string{
		LabelWorkspace: c.workspace,
		LabelNamespace: c.namespace,
		LabelAppName:   app.Name,
	}

	// Memory limit
	var memoryBytes int64
	if app.Resources != nil && app.Resources.Limits.Memory != "" {
		memoryBytes = parseMemory(app.Resources.Limits.Memory)
	}

	// SHM size
	var shmSize int64
	if app.ShmSize != "" {
		shmSize = parseMemory(app.ShmSize)
	}

	config := &container.Config{
		Image:        app.Image,
		Env:          env,
		Cmd:          app.Cmd,
		ExposedPorts: exposedPorts,
		Labels:       labels,
	}

	hostConfig := &container.HostConfig{
		Binds:        binds,
		PortBindings: portBindings,
		NetworkMode:  container.NetworkMode(networkName),
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}
	if memoryBytes > 0 {
		hostConfig.Resources.Memory = memoryBytes
	}
	if shmSize > 0 {
		hostConfig.ShmSize = shmSize
	}

	// Network aliases: app name + any additional aliases
	aliases := []string{app.Name}
	aliases = append(aliases, app.NetworkAliases...)

	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {
				Aliases: aliases,
			},
		},
	}

	resp, err := c.cli.ContainerCreate(ctx, config, hostConfig, networkConfig, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", containerName, err)
	}

	return resp.ID, nil
}

// StartContainer starts a container by ID.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// StopContainer stops a container with a timeout.
func (c *Client) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	timeout := timeoutSec
	return c.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	return c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// StopAndRemoveContainer stops and removes a container by name.
func (c *Client) StopAndRemoveContainer(ctx context.Context, name string) error {
	if err := c.StopContainer(ctx, name, 30); err != nil {
		slog.Debug("stop container", "name", name, "err", err)
	}
	return c.RemoveContainer(ctx, name)
}

// GetContainers returns containers for this namespace.
func (c *Client) GetContainers(ctx context.Context) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelWorkspace+"="+c.workspace),
			filters.Arg("label", LabelNamespace+"="+c.namespace),
		),
	})
}

// InspectContainer returns container details.
func (c *Client) InspectContainer(ctx context.Context, id string) (types.ContainerJSON, error) {
	return c.cli.ContainerInspect(ctx, id)
}

// PullImage pulls a Docker image.
func (c *Client) PullImage(ctx context.Context, img string) error {
	reader, err := c.cli.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", img, err)
	}
	defer reader.Close()
	// Consume the reader to completion
	_, err = io.Copy(io.Discard, reader)
	return err
}

// ImageExists checks if an image exists locally.
func (c *Client) ImageExists(ctx context.Context, img string) bool {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, img)
	return err == nil
}

// ContainerLogs returns logs from a container.
func (c *Client) ContainerLogs(ctx context.Context, containerID string, tail int) (string, error) {
	reader, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return stripDockerLogHeaders(string(data)), nil
}

// ExecInContainer runs a command inside a running container.
func (c *Client) ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, int, error) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", -1, err
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return "", -1, err
	}
	defer attachResp.Close()

	output, err := io.ReadAll(attachResp.Reader)
	if err != nil {
		return "", -1, err
	}

	// Get exit code
	inspectResp, err := c.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return stripDockerLogHeaders(string(output)), -1, err
	}

	return stripDockerLogHeaders(string(output)), inspectResp.ExitCode, nil
}

// ContainerStats returns resource stats for a container.
// GetPublishedPort returns the host port for a container's exposed port.
func (c *Client) GetPublishedPort(ctx context.Context, containerID string, containerPort int) int {
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return -1
	}
	for port, bindings := range inspect.NetworkSettings.Ports {
		if port.Int() == containerPort && len(bindings) > 0 {
			var hostPort int
			fmt.Sscanf(bindings[0].HostPort, "%d", &hostPort)
			return hostPort
		}
	}
	return -1
}

func (c *Client) ContainerStats(ctx context.Context, containerID string) (*ContainerStat, error) {
	resp, err := c.cli.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseContainerStats(resp.Body)
}

// CreateVolume creates a Docker volume.
func (c *Client) CreateVolume(ctx context.Context, name string) error {
	_, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	return err
}

// WaitForContainer waits for a container to start running.
func (c *Client) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for container %s", containerID)
		case <-ticker.C:
			inspect, err := c.cli.ContainerInspect(ctx, containerID)
			if err != nil {
				continue
			}
			if inspect.State.Running {
				return nil
			}
			if inspect.State.ExitCode != 0 {
				return fmt.Errorf("container exited with code %d", inspect.State.ExitCode)
			}
		}
	}
}

// WaitForContainerExit waits for a container to finish and exit (for init containers).
func (c *Client) WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for container exit %s", containerID)
		case <-ticker.C:
			inspect, err := c.cli.ContainerInspect(ctx, containerID)
			if err != nil {
				continue
			}
			if !inspect.State.Running {
				if inspect.State.ExitCode != 0 {
					return fmt.Errorf("init container exited with code %d", inspect.State.ExitCode)
				}
				return nil
			}
		}
	}
}

// parseMemory converts memory strings like "128m", "1g" to bytes.
func parseMemory(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "m"):
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "k"):
		multiplier = 1024
		s = s[:len(s)-1]
	}

	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n * multiplier
}

// stripDockerLogHeaders removes Docker log stream headers (8-byte prefix per line).
func stripDockerLogHeaders(s string) string {
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) >= 8 {
			result.WriteString(line[8:])
		} else {
			result.WriteString(line)
		}
		result.WriteString("\n")
	}
	return strings.TrimRight(result.String(), "\n")
}

// ContainerStat holds resource usage for a container.
type ContainerStat struct {
	CPUPercent float64
	MemUsage   int64
	MemLimit   int64
}
