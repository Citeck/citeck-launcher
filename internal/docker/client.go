package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/citeck/citeck-launcher/internal/appdef"
)

// Labels used to track Citeck containers.
// These must match the Kotlin desktop app labels (DockerLabels.kt) for backward compatibility.
const (
	LabelLauncher    = "citeck.launcher"           // marker label, always "true"
	LabelWorkspace   = "citeck.launcher.workspace"
	LabelNamespace   = "citeck.launcher.namespace"
	LabelAppName     = "citeck.launcher.app.name"  // Kotlin: DockerLabels.APP_NAME
	LabelAppHash     = "citeck.launcher.app.hash"  // Kotlin: DockerLabels.APP_HASH
	LabelOrigName    = "citeck.launcher.original-name"
	LabelComposeProj = "com.docker.compose.project" // Docker Desktop grouping
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
	// Named volumes (e.g. "mongo2:/data/db") are converted to bind mounts
	// at {volumesBaseDir}/{name} so data stays in the runtime directory.
	var binds []string
	for _, v := range app.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 {
			source := parts[0]
			if strings.HasPrefix(source, "./") && volumesBaseDir != "" {
				// Relative path — resolve against volumesBase
				v = filepath.Join(volumesBaseDir, source[2:]) + ":" + parts[1]
			} else if !strings.ContainsAny(source, "/.") && volumesBaseDir != "" {
				// Named volume — convert to bind mount in runtime dir
				hostDir := filepath.Join(volumesBaseDir, "volumes", source)
				os.MkdirAll(hostDir, 0o755)
				v = hostDir + ":" + parts[1]
			}
		}
		binds = append(binds, v)
	}

	// Labels (must match Kotlin DockerLabels for backward compatibility)
	labels := map[string]string{
		LabelLauncher:    "true",
		LabelWorkspace:   c.workspace,
		LabelNamespace:   c.namespace,
		LabelAppName:     app.Name,
		LabelAppHash:     app.GetHash(),
		LabelComposeProj: fmt.Sprintf("citeck_launcher_%s_%s", c.namespace, c.workspace),
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

	// Init containers should not have a restart policy — only main containers
	restartPolicy := container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	if app.IsInit {
		restartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}

	hostConfig := &container.HostConfig{
		Binds:         binds,
		PortBindings:  portBindings,
		NetworkMode:   container.NetworkMode(networkName),
		RestartPolicy: restartPolicy,
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
	if err := c.StopContainer(ctx, name, 10); err != nil {
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

// ListAllLauncherContainers returns ALL containers with the citeck.launcher label,
// regardless of workspace/namespace. Used by the clean command to find orphans.
func (c *Client) ListAllLauncherContainers(ctx context.Context) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelLauncher+"=true"),
		),
	})
}

// CleanupStaleContainers stops and removes all containers from this namespace.
// Used at startup to clear leftovers from a previous daemon run.
func (c *Client) CleanupStaleContainers(ctx context.Context) {
	containers, err := c.GetContainers(ctx)
	if err != nil {
		return
	}
	for _, ctr := range containers {
		name := ctr.Names[0]
		if strings.HasPrefix(name, "/") {
			name = name[1:]
		}
		slog.Info("Removing stale container", "name", name)
		_ = c.StopAndRemoveContainer(ctx, name)
	}
}

// InspectContainer returns container details.
func (c *Client) InspectContainer(ctx context.Context, id string) (types.ContainerJSON, error) {
	return c.cli.ContainerInspect(ctx, id)
}

// RegistryAuth holds credentials for a Docker registry.
type RegistryAuth struct {
	Username string
	Password string
	Registry string // e.g. "https://harbor.citeck.ru"
}

// PullImage pulls a Docker image, optionally with registry credentials.
func (c *Client) PullImage(ctx context.Context, img string, auth *RegistryAuth) error {
	opts := image.PullOptions{}
	if auth != nil && auth.Username != "" {
		authCfg := registry.AuthConfig{
			Username:      auth.Username,
			Password:      auth.Password,
			ServerAddress: auth.Registry,
		}
		encoded, err := registry.EncodeAuthConfig(authCfg)
		if err != nil {
			return fmt.Errorf("encode auth for %s: %w", img, err)
		}
		opts.RegistryAuth = encoded
	}
	reader, err := c.cli.ImagePull(ctx, img, opts)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", img, err)
	}
	defer reader.Close()
	// Consume the reader to completion
	_, err = io.Copy(io.Discard, reader)
	return err
}

// ContainerLogsFollow returns a streaming reader for container logs with follow=true.
// The caller must close the returned reader.
func (c *Client) ContainerLogsFollow(ctx context.Context, containerID string, tail int) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       fmt.Sprintf("%d", tail),
	})
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

	// Use stdcopy to properly demultiplex Docker log stream headers
	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, reader)
	if err != nil {
		// Fallback: some containers use TTY mode (no multiplex headers)
		reader2, err2 := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true, Tail: fmt.Sprintf("%d", tail),
		})
		if err2 != nil {
			return "", err2
		}
		defer reader2.Close()
		data, _ := io.ReadAll(reader2)
		return string(data), nil
	}

	result := stdout.String()
	if s := stderr.String(); s != "" {
		result += s
	}
	return result, nil
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

	// Demultiplex exec output
	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		// Fallback for TTY-mode exec
		data, _ := io.ReadAll(attachResp.Reader)
		output := string(data)
		inspectResp, _ := c.cli.ContainerExecInspect(ctx, execResp.ID)
		return output, inspectResp.ExitCode, nil
	}

	output := stdout.String() + stderr.String()

	// Get exit code
	inspectResp, err := c.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return output, -1, err
	}

	return output, inspectResp.ExitCode, nil
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

// WaitForContainer waits for a container to start running.
func (c *Client) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Quick check if already running
	inspect, err := c.cli.ContainerInspect(timeoutCtx, containerID)
	if err == nil && inspect.State.Running {
		return nil
	}

	// Poll with short interval — ContainerWait only supports "not-running" condition
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for container %s", containerID)
		case <-ticker.C:
			inspect, err := c.cli.ContainerInspect(timeoutCtx, containerID)
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
// Uses Docker's ContainerWait API for instant notification instead of polling.
func (c *Client) WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	waitCh, errCh := c.cli.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)
	select {
	case <-timeoutCtx.Done():
		return fmt.Errorf("timeout waiting for container exit %s", containerID)
	case err := <-errCh:
		return fmt.Errorf("wait error for %s: %w", containerID, err)
	case result := <-waitCh:
		if result.StatusCode != 0 {
			return fmt.Errorf("init container exited with code %d", result.StatusCode)
		}
		return nil
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

// ContainerStat holds resource usage for a container.
type ContainerStat struct {
	CPUPercent float64
	MemUsage   int64
	MemLimit   int64
}

// RunUtilsContainer runs a command in a temporary launcher-utils container with the given bind mounts.
// It creates the container, starts it, waits for exit, captures output, and removes it.
func (c *Client) RunUtilsContainer(ctx context.Context, cmd []string, binds []string) (string, int, error) {
	utilsImage := config.UtilsImage()

	containerName := c.ContainerName("launcher-utils-tmp")
	_ = c.StopAndRemoveContainer(ctx, containerName)

	resp, err := c.cli.ContainerCreate(ctx, &container.Config{
		Image: utilsImage,
		Cmd:   cmd,
		Labels: map[string]string{
			LabelLauncher:  "true",
			LabelWorkspace: c.workspace,
			LabelNamespace: c.namespace,
			LabelAppName:   "launcher-utils",
		},
	}, &container.HostConfig{
		Binds: binds,
	}, nil, nil, containerName)
	if err != nil {
		return "", -1, fmt.Errorf("create utils container: %w", err)
	}
	containerID := resp.ID
	defer func() { _ = c.RemoveContainer(ctx, containerID) }()

	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", -1, fmt.Errorf("start utils container: %w", err)
	}

	if err := c.WaitForContainerExit(ctx, containerID, 5*time.Minute); err != nil {
		return "", -1, fmt.Errorf("wait for utils container: %w", err)
	}

	output, _ := c.ContainerLogs(ctx, containerID, 1000)

	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return output, -1, nil
	}
	return output, inspect.State.ExitCode, nil
}

