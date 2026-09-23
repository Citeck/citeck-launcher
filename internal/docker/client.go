package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

// Labels used to track Citeck containers.
// These must match the Kotlin desktop app labels (DockerLabels.kt) for backward compatibility.
const (
	LabelLauncher    = "citeck.launcher" // marker label, always "true"
	LabelWorkspace   = "citeck.launcher.workspace"
	LabelNamespace   = "citeck.launcher.namespace"
	LabelAppName     = "citeck.launcher.app.name" // Kotlin: DockerLabels.APP_NAME
	LabelAppHash     = "citeck.launcher.app.hash" // Kotlin: DockerLabels.APP_HASH
	LabelOrigName    = "citeck.launcher.original-name"
	LabelComposeProj = "com.docker.compose.project" // Docker Desktop grouping
	// LabelTemp marks a container the launcher created for one of its OWN
	// operations (a dependency migration's source/target cluster), not a
	// namespace app. It carries the namespace labels too, so PurgeNamespace
	// removes it with everything else, but nothing adopts it as an app.
	LabelTemp = "citeck.launcher.temp"
	// LabelTempValue is the value LabelTemp is set to. Docker's label filter
	// matches a bare key as "present with ANY value", so every filter and
	// every lookup in the launcher is key-only and does not depend on this —
	// but a writer must set it, and must set it through this constant, so the
	// two halves cannot drift into "temp=1" vs "temp=true".
	LabelTempValue = "true"
)

// Client wraps the Docker SDK client with Citeck-specific operations.
type Client struct {
	cli       *client.Client
	workspace string // empty in server mode, set in desktop mode for Kotlin compat
	namespace string
	// desktopVolumes selects the naming strategy for plain named-volume
	// sources in CreateContainer: true = (ns, ws)-scoped Docker named
	// volumes (Kotlin desktop parity), false = bind mounts under the
	// runtime dir (server mode). Injected via WithDesktopVolumeNaming;
	// defaults to config.IsDesktopMode(), read ONCE in NewClient.
	desktopVolumes bool
}

// Option customizes a Client at construction time.
type Option func(*Client)

// WithDesktopVolumeNaming sets the naming strategy for plain named-volume
// sources (e.g. "mongo2:/data/db"): desktop=true scopes them as Docker named
// volumes per (namespace, workspace); desktop=false converts them to bind
// mounts under the runtime volumes dir. When the option is not supplied,
// NewClient falls back to config.IsDesktopMode() so existing call sites keep
// their behavior until they inject the mode explicitly.
func WithDesktopVolumeNaming(desktop bool) Option {
	return func(c *Client) { c.desktopVolumes = desktop }
}

// NewClient creates a Docker client.
// workspace is used in container/network names for desktop mode backward compatibility.
// Pass "" for server mode (names become citeck_{app}_{ns}).
func NewClient(workspace, namespace string, options ...Option) (*Client, error) {
	// API-version negotiation is the default in the moby client; FromEnv keeps
	// honoring DOCKER_HOST / DOCKER_API_VERSION / DOCKER_CERT_PATH overrides.
	opts := []client.Opt{client.FromEnv}

	// If DOCKER_HOST is not set, honor the active docker CLI context first
	// (this is what `docker` itself does — on macOS Docker Desktop the default
	// "desktop-linux" context points at ~/.docker/run/docker.sock, and colima /
	// Rancher Desktop use their own paths), then fall back to scanning common
	// socket locations. Never hardcode a single path.
	if os.Getenv("DOCKER_HOST") == "" {
		if host := dockerHostFromContext(); host != "" {
			opts = append(opts, client.WithHost(host))
		} else if socketPath := detectDockerSocket(); socketPath != "" {
			opts = append(opts, client.WithHost("unix://"+socketPath))
		}
	}

	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	// Default the volume-naming strategy from the process-global mode flag —
	// read once here (not per CreateContainer call) so the low-level wrapper
	// stays deterministic after construction and callers can override it.
	c := &Client{cli: cli, workspace: workspace, namespace: namespace, desktopVolumes: config.IsDesktopMode()}
	for _, opt := range options {
		opt(c)
	}
	return c, nil
}

// dockerHostFromContext resolves the Docker endpoint from the active docker CLI
// context — $DOCKER_CONTEXT, else "currentContext" in ~/.docker/config.json.
// This mirrors what the docker CLI does and adapts to whatever the user's setup
// actually is (Docker Desktop's "desktop-linux", colima, Rancher Desktop, …)
// instead of guessing a fixed socket path. Returns "" when there is no usable
// context so the caller can fall back to socket auto-detection.
func dockerHostFromContext() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		data, readErr := os.ReadFile(filepath.Join(home, ".docker", "config.json")) //nolint:gosec // user's own docker config
		if readErr != nil {
			return ""
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if json.Unmarshal(data, &cfg) != nil {
			return ""
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return ""
	}
	// Docker stores each context under a directory named by the hex SHA-256 of
	// the context name (see docker/cli context store digest scheme).
	sum := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(home, ".docker", "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(metaPath) //nolint:gosec // path derived from docker's own context store
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	host := meta.Endpoints.Docker.Host
	// For a unix endpoint, only trust the context if the socket really exists;
	// otherwise fall through to detectDockerSocket. Non-unix hosts (tcp://, …)
	// are returned as-is for the client to dial.
	if sock, ok := strings.CutPrefix(host, "unix://"); ok {
		if fi, statErr := os.Stat(sock); statErr != nil || fi.Mode()&os.ModeSocket == 0 {
			return ""
		}
	}
	return host
}

// detectDockerSocket finds the Docker socket in common locations. This is the
// fallback used only when there is no DOCKER_HOST and no usable docker context;
// the list is ordered most- to least-common across the supported platforms.
func detectDockerSocket() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/var/run/docker.sock",                                   // default daemon / Docker Desktop default-socket opt-in
		filepath.Join(home, ".docker", "run", "docker.sock"),     // Docker Desktop user socket (macOS)
		fmt.Sprintf("/run/user/%d/docker.sock", os.Getuid()),     // rootless Linux
		filepath.Join(home, ".docker", "desktop", "docker.sock"), // legacy Docker Desktop
	}
	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 { //nolint:gosec // well-known socket paths
			return path
		}
	}
	return ""
}

// Close releases the Docker client connection.
func (c *Client) Close() error {
	return c.cli.Close() //nolint:wrapcheck // transparent close
}

// Namespace returns the namespace this client is scoped to. Used to enforce the
// invariant that the daemon's active client always matches the active namespace
// (see loadNamespace / installLoadedNamespace) — a mismatch is the root of the
// "containers carry the wrong namespace id" class of bugs.
func (c *Client) Namespace() string { return c.namespace }

// Workspace returns the workspace this client is scoped to ("" in server mode).
func (c *Client) Workspace() string { return c.workspace }

// Ping checks Docker connectivity.
func (c *Client) Ping(ctx context.Context) error {
	if _, err := c.cli.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// ContainerName generates the Docker container name.
// Server mode (workspace=""): citeck_{app}_{ns}
// Desktop mode (workspace set): citeck_{app}_{ns}_{ws} (Kotlin backward compat)
func (c *Client) ContainerName(appName string) string {
	if c.workspace == "" {
		return fmt.Sprintf("citeck_%s_%s", appName, c.namespace)
	}
	return fmt.Sprintf("citeck_%s_%s_%s", appName, c.namespace, c.workspace)
}

// NetworkName returns the Docker network name for this namespace.
func (c *Client) NetworkName() string {
	if c.workspace == "" {
		return fmt.Sprintf("citeck_network_%s", c.namespace)
	}
	return fmt.Sprintf("citeck_network_%s_%s", c.namespace, c.workspace)
}

func (c *Client) composeProject() string {
	if c.workspace == "" {
		return fmt.Sprintf("citeck_launcher_%s", c.namespace)
	}
	return fmt.Sprintf("citeck_launcher_%s_%s", c.namespace, c.workspace)
}

// CreateNetwork creates a bridge network for the namespace.
func (c *Client) CreateNetwork(ctx context.Context) (string, error) {
	name := c.NetworkName()

	// Check if exists
	networks, err := c.cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("name", name),
	})
	if err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	for _, n := range networks.Items {
		if n.Name == name {
			return n.ID, nil
		}
	}

	resp, err := c.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			LabelLauncher:  "true",
			LabelWorkspace: c.workspace,
			LabelNamespace: c.namespace,
		},
	})
	if err != nil {
		return "", fmt.Errorf("create network %s: %w", name, err)
	}
	return resp.ID, nil
}

// RemoveNetwork removes the namespace network.
func (c *Client) RemoveNetwork(ctx context.Context) error {
	if _, err := c.cli.NetworkRemove(ctx, c.NetworkName(), client.NetworkRemoveOptions{}); err != nil {
		return fmt.Errorf("remove network %s: %w", c.NetworkName(), err)
	}
	return nil
}

// IsStaleNetworkError reports whether err is the Docker "network still has
// active endpoints" error — the daemon returns this when a network removal
// races a still-running container. Callers treat this as a soft / informational
// outcome rather than a real failure, since the network will be reclaimed
// either by a subsequent reconcile pass or by Docker once the last endpoint
// disconnects.
func IsStaleNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "has active endpoints") {
		return true
	}
	if strings.Contains(msg, "endpoint") && strings.Contains(msg, "is in use") {
		return true
	}
	return false
}

// RemoveNetworkByName removes a Docker network by its exact name.
func (c *Client) RemoveNetworkByName(ctx context.Context, name string) error {
	if _, err := c.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

// ListLauncherNetworks returns networks with the citeck.launcher label.
func (c *Client) ListLauncherNetworks(ctx context.Context) ([]network.Summary, error) {
	result, err := c.cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("label", LabelLauncher+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list launcher networks: %w", err)
	}
	return result.Items, nil
}

// ContainerCreateOpts customizes CreateContainerWith. The zero value is the
// namespace app container CreateContainer has always created.
type ContainerCreateOpts struct {
	// Name overrides the container name (scoped through ContainerName) and,
	// with it, the LabelAppName label; "" = app.Name.
	Name string
	// ExtraLabels are merged over the standard launcher labels.
	ExtraLabels map[string]string
	// NoRestart creates the container with restart policy "no", so Docker
	// never brings it back on its own.
	//
	// It exists for containers that belong to ONE launcher operation rather
	// than to the namespace: a dependency migration's temp containers. The
	// default (unless-stopped) is right for an app the launcher intends to
	// keep running and wrong for those — a launcher killed mid-migration, or a
	// host reboot, would have Docker restart citeck_depsmig-dst_<ns>
	// indefinitely with the TARGET data volume mounted, and the next start of
	// the namespace's own postgres would put a SECOND server on that same data
	// directory. Nothing reads LabelTemp yet, so the policy is the only thing
	// standing between an interrupted migration and two servers contending for
	// one PGDATA.
	NoRestart bool
	// ExtraHosts are "<hostname>:<ip>" entries written into the container's
	// own /etc/hosts (Docker's --add-host).
	//
	// It exists for one measured reason. RabbitMQ derives its node name from
	// the container hostname and its data directory contains that node name,
	// so a temp container started under an override name boots a fresh EMPTY
	// node inside the data volume and reports healthy. Pinning the node
	// identity takes RABBITMQ_NODENAME in the environment AND an /etc/hosts
	// entry for the host part of that node name — the environment alone fails
	// the boot with "epmd error for host rabbitmq: nxdomain".
	//
	// The reason it is /etc/hosts and not a Hostname override is the other
	// half: moby registers a container's hostname as a DNS name on a
	// user-defined network, so overriding it would make a temp container
	// answer to the app's own name on the namespace network. An /etc/hosts
	// entry is container-local and invisible to that DNS.
	ExtraHosts []string
}

// CreateContainer creates a container from an ApplicationDef.
func (c *Client) CreateContainer(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string) (string, error) {
	return c.CreateContainerWith(ctx, app, volumesBaseDir, ContainerCreateOpts{})
}

// CreateContainerWith is CreateContainer with a name override and extra
// labels — what a dependency migration needs to run the namespace's real
// postgres def under another name, next to (not instead of) the namespace's
// own container.
//
// Everything except the engine call itself lives in buildCreateOptions, so the
// wiring of opts into the four places it has to reach can be pinned without a
// live Docker daemon.
func (c *Client) CreateContainerWith(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string, opts ContainerCreateOpts) (string, error) {
	createOpts, err := c.buildCreateOptions(ctx, app, volumesBaseDir, opts)
	if err != nil {
		return "", err
	}

	resp, err := c.cli.ContainerCreate(ctx, createOpts)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", createOpts.Name, err)
	}

	return resp.ID, nil
}

// buildCreateOptions assembles the whole ContainerCreate request for app under
// opts: the container name, config, host config and networking config, plus
// the bind-mount sources it has to materialize on the host.
//
// It is a seam, not just a helper. CreateContainerWith has to hand its OWN
// opts to four different builders — the name, the labels, the host config and
// the network aliases — and each builder was unit-tested on its own while
// nothing checked that the create path actually passes opts to all of them.
// Dropping opts at any one of those call sites reproduces exactly what the
// override exists to prevent (a temp container Docker restarts after a reboot,
// or one that answers to "postgres" on the namespace network) with every unit
// test still green.
func (c *Client) buildCreateOptions(
	ctx context.Context,
	app appdef.ApplicationDef,
	volumesBaseDir string,
	opts ContainerCreateOpts,
) (client.ContainerCreateOptions, error) {
	name, overridden := effectiveName(app, opts)
	// An override spelled exactly like the app's own name is refused rather
	// than served. It would produce a container named and labeled like the
	// namespace's own app — LabelAppName is how buildExistingContainerMap and
	// the reconciler index containers — so the caller's temp container would be
	// ADOPTED as the app's. Docker's name conflict is not a guard we may lean
	// on: it only fires while the app's container exists, and a migration runs
	// with the namespace stopped, where a removed or never-created container
	// leaves the name free.
	if overridden && name == app.Name {
		return client.ContainerCreateOptions{}, fmt.Errorf(
			"container name override %q is the app's own name: use CreateContainer for the namespace's own container", name)
	}
	containerName := c.ContainerName(name)
	networkName := c.NetworkName()

	// Environment
	env := make([]string, 0, app.Environments.Len())
	for _, e := range app.Environments {
		env = append(env, e.Key+"="+e.Value)
	}

	// Port bindings
	exposedPorts := network.PortSet{}
	portBindings := network.PortMap{}
	for _, p := range app.Ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPort := parts[0]
		// The spec's own protocol is kept ("17014/udp"); none means tcp, which
		// ParsePort applies itself. Appending "/tcp" unconditionally, as this
		// used to, turned the observer's UDP log receiver into the port
		// "17014/udp/tcp" — which the engine accepts at create and refuses at
		// START ("programming external connectivity ...: unknown protocol").
		containerPort, err := network.ParsePort(parts[1])
		if err != nil {
			return client.ContainerCreateOptions{}, fmt.Errorf("invalid container port %q for %s: %w", parts[1], app.Name, err)
		}
		// ParsePort does not validate the protocol, so a bad one would again
		// only fail at start, as an engine error naming no app. Refuse it here.
		switch containerPort.Proto() {
		case network.TCP, network.UDP, network.SCTP:
		default:
			return client.ContainerCreateOptions{}, fmt.Errorf("invalid container port %q for %s: unknown protocol %q",
				parts[1], app.Name, containerPort.Proto())
		}
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []network.PortBinding{{HostPort: hostPort}}
	}

	// Volumes (binds)
	// Named volumes (e.g. "mongo2:/data/db") are converted to bind mounts
	// at {volumesBaseDir}/{name} so data stays in the runtime directory.
	binds := make([]string, 0, len(app.Volumes))
	for _, v := range app.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 { //nolint:nestif // volume source resolution logic
			source := parts[0]
			if strings.HasPrefix(source, "./") && volumesBaseDir != "" {
				// Relative path — resolve against volumesBase.
				// Ensure the parent directory exists so Docker doesn't create
				// a directory at the file path (its default for missing bind sources).
				hostPath := filepath.Join(volumesBaseDir, source[2:])
				if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil { //nolint:gosec // Docker bind-mount dirs need container-accessible perms
					return client.ContainerCreateOptions{}, fmt.Errorf("create bind-mount directory %s: %w", filepath.Dir(hostPath), err)
				}
				v = hostPath + ":" + parts[1]
			} else if !strings.ContainsAny(source, "/.") && volumesBaseDir != "" && !c.desktopVolumes {
				// Server mode: convert named volume to bind mount in runtime dir.
				hostDir := filepath.Join(volumesBaseDir, "volumes", source)
				if err := os.MkdirAll(hostDir, 0o755); err != nil { //nolint:gosec // Docker bind-mount dirs need container-accessible perms
					return client.ContainerCreateOptions{}, fmt.Errorf("create bind-mount directory %s: %w", hostDir, err)
				}
				v = hostDir + ":" + parts[1]
			} else if !strings.ContainsAny(source, "/.") && c.desktopVolumes {
				// Desktop mode: scope the named volume per (ns, ws) so two
				// namespaces with the same plain volume name don't collide on
				// one Docker volume. Matches Kotlin DockerApi.createVolume.
				scopedName, err := c.CreateVolume(ctx, source)
				if err != nil {
					return client.ContainerCreateOptions{}, err
				}
				v = scopedName + ":" + parts[1]
			}
		}
		binds = append(binds, v)
	}

	labels := c.containerLabels(app, name, opts.ExtraLabels)

	// Memory limit
	var memoryBytes int64
	if app.Resources != nil && app.Resources.Limits.Memory != "" {
		memoryBytes = ParseMemory(app.Resources.Limits.Memory)
	}

	// SHM size
	var shmSize int64
	if app.ShmSize != "" {
		shmSize = ParseMemory(app.ShmSize)
	}

	ctrConfig := buildContainerConfig(app, name, env, exposedPorts, labels)
	hostConfig := buildHostConfig(app, opts, binds, portBindings, networkName, memoryBytes, shmSize)

	aliases := networkAliases(app, name, overridden)

	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {
				Aliases: aliases,
			},
		},
	}

	return client.ContainerCreateOptions{
		Config:           ctrConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkConfig,
		Name:             containerName,
	}, nil
}

// effectiveName is the name a container is created UNDER: the override when
// one is given, else the app's own name. It is the single source for all three
// places that name has to appear — the Docker container name, the LabelAppName
// label and the primary network alias — because a disagreement between them is
// precisely the accident this seam exists to prevent: a temp container created
// as citeck_postgres_<ns> would collide with (or, worse, be adopted as) the
// namespace's real postgres container.
//
// It reports whether the name CAME FROM an override, and every decision that
// depends on "is this the app's own container?" takes that boolean rather than
// comparing the result with app.Name. The comparison is not the same question:
// an override a caller spelled exactly like the app's name is still an
// override, and answering it as "no override" would hand a foreign container
// the app's whole DNS identity.
func effectiveName(app appdef.ApplicationDef, opts ContainerCreateOpts) (name string, overridden bool) {
	if opts.Name != "" {
		return opts.Name, true
	}
	return app.Name, false
}

// networkAliases is the DNS identity the container takes on the namespace
// network: its effective name plus the def's additional aliases.
//
// Under a name override BOTH of the app's own identities are dropped — its
// name and its extra aliases — and the container answers only to the override.
// Docker permits duplicate aliases on one network and round-robins between the
// endpoints that hold them, so a temp container that also answered to
// "postgres" would silently take a share of the real container's traffic, one
// connection at a time and with no error anywhere. Clearing
// def.NetworkAliases at the call site would not have been enough: the primary
// alias comes from app.Name, not from that field.
//
// overridden is effectiveName's answer, not a comparison of name against
// app.Name: the two differ exactly when a caller overrode the name with the
// app's own spelling, and treating that as "no override" is what would give a
// foreign container the app's aliases.
func networkAliases(app appdef.ApplicationDef, name string, overridden bool) []string {
	if overridden {
		return []string{name}
	}
	aliases := make([]string, 0, 1+len(app.NetworkAliases))
	aliases = append(aliases, app.Name)
	aliases = append(aliases, app.NetworkAliases...)
	return aliases
}

// containerLabels builds the launcher labels for a container (they must match
// Kotlin DockerLabels for backward compatibility). LabelWorkspace holds the
// workspace ID (Kotlin contract); in server mode it is empty, and the label is
// set to "" rather than mis-attributed to the namespace value, which would
// falsely identify containers as belonging to a workspace named after their
// namespace.
//
// name is the EFFECTIVE container name, which for a temp container is the
// override rather than app.Name — and it must land in LabelAppName, because
// that label is how the runtime indexes the namespace's containers
// (buildExistingContainerMap, runReconcileDiffTask). A leftover temp container
// still labeled with the app's name would be adopted as THE app's container.
// The namespace/workspace labels stay as they are, so PurgeNamespace — which
// filters on LabelNamespace plus the workspace, never on LabelAppName — still
// removes it with everything else.
func (c *Client) containerLabels(app appdef.ApplicationDef, name string, extra map[string]string) map[string]string {
	// The extra labels are seeded FIRST and the launcher's own are written over
	// them, so a caller can add labels but can never take one away. The
	// direction matters most for LabelNamespace: PurgeNamespace finds a
	// container only by that label, so a container that lost it would survive
	// the purge holding a data volume, invisible to every launcher surface —
	// exactly the leak a temp container must not be able to cause.
	labels := make(map[string]string, len(extra)+6)
	maps.Copy(labels, extra)
	labels[LabelLauncher] = "true"
	labels[LabelWorkspace] = c.workspace
	labels[LabelNamespace] = c.namespace
	labels[LabelAppName] = name
	labels[LabelAppHash] = app.GetHash()
	labels[LabelComposeProj] = c.composeProject()
	return labels
}

// buildContainerConfig assembles the container.Config for app. It sets
// Hostname to the EFFECTIVE container name (Kotlin 1.x parity —
// AppStartAction.withHostName). Docker otherwise defaults the hostname to the
// container ID, which for images that derive identity from the hostname —
// notably RabbitMQ, whose node name is rabbit@<hostname> and whose Mnesia data
// lives under mnesia/rabbit@<hostname>/ — means every container recreate lands
// in a fresh data dir and silently abandons the previous state (users,
// permissions, queues).
//
// It is the effective name and not app.Name for the same reason the network
// aliases are (see networkAliases): moby registers a container's HOSTNAME as a
// DNS name on a user-defined network, so a temp container created under an
// override would still answer to "postgres" on the namespace network and take
// a share of the real container's traffic, one connection at a time and with
// no error anywhere. Without an override the two are the same string, so the
// namespace's own containers are unaffected.
func buildContainerConfig(
	app appdef.ApplicationDef,
	name string,
	env []string,
	exposedPorts network.PortSet,
	labels map[string]string,
) *container.Config {
	return &container.Config{
		Hostname:     name,
		Image:        app.Image,
		Env:          env,
		Cmd:          app.Cmd,
		ExposedPorts: exposedPorts,
		Labels:       labels,
	}
}

// restartPolicyFor is the restart policy a container is created with: none for
// an init container (it is meant to run once) and none for a caller that asked
// for NoRestart (see ContainerCreateOpts — a container belonging to one
// launcher operation must never outlive it on Docker's initiative); every
// other container restarts unless-stopped, which is what keeps a namespace up
// across a host reboot.
func restartPolicyFor(app appdef.ApplicationDef, opts ContainerCreateOpts) container.RestartPolicy {
	if app.IsInit || opts.NoRestart {
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
	return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
}

// buildHostConfig assembles the container.HostConfig for app. The restart
// policy is restartPolicyFor's. When a memory limit is set, swap is pinned
// equal to it (MemorySwap == Memory) so the limit is a hard RAM cap with NO
// swap — Kotlin 1.x parity (AppStartAction.withMemorySwap(memory)). Docker
// otherwise defaults MemorySwap to 2×Memory, letting a capped container spill
// into swap (thrashing instead of a clean cap; bad for brokers and DBs).
func buildHostConfig(
	app appdef.ApplicationDef,
	opts ContainerCreateOpts,
	binds []string,
	portBindings network.PortMap,
	networkName string,
	memoryBytes, shmSize int64,
) *container.HostConfig {
	hostConfig := &container.HostConfig{
		Binds:         binds,
		PortBindings:  portBindings,
		NetworkMode:   container.NetworkMode(networkName),
		RestartPolicy: restartPolicyFor(app, opts),
		ExtraHosts:    opts.ExtraHosts,
		LogConfig: container.LogConfig{
			Type: "json-file",
			Config: map[string]string{
				"max-size": "50m",
				"max-file": "3",
			},
		},
	}
	if memoryBytes > 0 {
		hostConfig.Memory = memoryBytes
		hostConfig.MemorySwap = memoryBytes
	}
	if shmSize > 0 {
		hostConfig.ShmSize = shmSize
	}
	return hostConfig
}

// StartContainer starts a container by ID.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	if _, err := c.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

// StopContainer stops a container with a timeout.
func (c *Client) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	timeout := timeoutSec
	if _, err := c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

// RemoveContainer removes a container. Removing a container that no longer
// exists is treated as success (idempotent): a not-found result means the
// desired end state — container gone — is already achieved. Without this, a
// stop plan that targets an absent container (e.g. a never-created "<app>-init"
// during a mid-start stop) would fail, mask a real leak as STOPPING_FAILED, and
// abort the rest of the stop.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	if _, err := c.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}

// StopAndRemoveContainer stops and removes a container by name.
// timeoutSec is the stop timeout in seconds; 0 uses default (15s).
func (c *Client) StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	if err := c.StopContainer(ctx, name, timeoutSec); err != nil {
		slog.Debug("stop container", "name", name, "err", err)
	}
	return c.RemoveContainer(ctx, name)
}

// GetContainers returns containers for this namespace.
func (c *Client) GetContainers(ctx context.Context) ([]container.Summary, error) {
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", LabelNamespace+"="+c.namespace),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return result.Items, nil
}

// ListAllLauncherContainers returns ALL containers with the citeck.launcher label,
// regardless of workspace/namespace. Used by the clean command to find orphans.
func (c *Client) ListAllLauncherContainers(ctx context.Context) ([]container.Summary, error) {
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", LabelLauncher+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list all launcher containers: %w", err)
	}
	return result.Items, nil
}

// CleanupStaleContainers stops and removes all containers from this namespace.
// Used at startup to clear leftovers from a previous daemon run.
func (c *Client) CleanupStaleContainers(ctx context.Context) {
	containers, err := c.GetContainers(ctx)
	if err != nil {
		return
	}
	for _, ctr := range containers {
		if len(ctr.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(ctr.Names[0], "/")
		slog.Info("Removing stale container", "name", name)
		_ = c.StopAndRemoveContainer(ctx, name, 0)
	}
}

// InspectContainer returns container details.
func (c *Client) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	resp, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	return resp.Container, err //nolint:wrapcheck // thin Docker SDK wrapper
}

// RegistryAuth holds credentials for a Docker registry.
type RegistryAuth struct {
	Username string
	Password string
	Registry string // e.g. "https://harbor.citeck.ru"
}

// PullImage pulls a Docker image, optionally with registry credentials.
func (c *Client) PullImage(ctx context.Context, img string, auth *RegistryAuth) error {
	return c.PullImageWithProgress(ctx, img, auth, nil)
}

// PullProgressFn is called during image pull with current/total bytes and percentage.
type PullProgressFn func(currentMB, totalMB float64, pct int)

// PullImageWithProgress pulls an image and reports download progress via callback.
func (c *Client) PullImageWithProgress(ctx context.Context, img string, auth *RegistryAuth, progressFn PullProgressFn) error {
	opts := client.ImagePullOptions{}
	if auth != nil && auth.Username != "" {
		authCfg := registry.AuthConfig{
			Username:      auth.Username,
			Password:      auth.Password,
			ServerAddress: auth.Registry,
		}
		encoded, err := authconfig.Encode(authCfg)
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

	if progressFn == nil {
		_, err = io.Copy(io.Discard, reader)
		return err //nolint:wrapcheck // transparent I/O drain
	}

	return parsePullProgress(reader, progressFn)
}

// ContainerLogsFollow returns a streaming reader for container logs with follow=true.
// The caller must close the returned reader.
func (c *Client) ContainerLogsFollow(ctx context.Context, containerID string, tail int) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{ //nolint:wrapcheck // thin Docker SDK wrapper
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       fmt.Sprintf("%d", tail),
	})
}

// ImageExists checks if an image exists locally.
func (c *Client) ImageExists(ctx context.Context, img string) bool {
	_, err := c.cli.ImageInspect(ctx, img)
	return err == nil
}

// GetImageDigest returns the RepoDigest for a locally available image.
// Returns empty string if the image is not found or has no digest.
func (c *Client) GetImageDigest(ctx context.Context, img string) string {
	inspect, err := c.cli.ImageInspect(ctx, img)
	if err != nil {
		return ""
	}
	if len(inspect.RepoDigests) > 0 {
		return inspect.RepoDigests[0]
	}
	return ""
}

// ImageInfo is a trimmed, UI-facing view of a local image's inspect data.
type ImageInfo struct {
	Present      bool     `json:"present"`
	ID           string   `json:"id"` // "sha256:..."
	RepoTags     []string `json:"repoTags,omitempty"`
	RepoDigests  []string `json:"repoDigests,omitempty"`
	Size         int64    `json:"size"`
	OS           string   `json:"os,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Created      string   `json:"created,omitempty"`
}

// InspectImageInfo inspects a local image. A missing image (or any inspect
// error) yields ImageInfo{Present: false} with a nil error — the caller treats
// "not present" and "couldn't read" identically (offer a Pull either way).
func (c *Client) InspectImageInfo(ctx context.Context, img string) ImageInfo {
	insp, err := c.cli.ImageInspect(ctx, img)
	if err != nil {
		return ImageInfo{Present: false}
	}
	return ImageInfo{
		Present:      true,
		ID:           insp.ID,
		RepoTags:     insp.RepoTags,
		RepoDigests:  insp.RepoDigests,
		Size:         insp.Size,
		OS:           insp.Os,
		Architecture: insp.Architecture,
		Created:      insp.Created,
	}
}

// PruneUnusedImages removes dangling images and returns space reclaimed in bytes.
func (c *Client) PruneUnusedImages(ctx context.Context) (uint64, error) {
	report, err := c.cli.ImagePrune(ctx, client.ImagePruneOptions{
		Filters: make(client.Filters).Add("dangling", "true"),
	})
	if err != nil {
		return 0, fmt.Errorf("prune images: %w", err)
	}
	return report.Report.SpaceReclaimed, nil
}

// parsePullProgress reads Docker pull JSON stream and reports aggregated progress.
func parsePullProgress(reader io.Reader, progressFn PullProgressFn) error {
	type pullEvent struct {
		Status         string `json:"status"`
		ProgressDetail struct {
			Current int64 `json:"current"`
			Total   int64 `json:"total"`
		} `json:"progressDetail"`
		ID    string `json:"id"`
		Error string `json:"error"`
	}

	layerCurrent := make(map[string]int64)
	layerTotal := make(map[string]int64)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var evt pullEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		if evt.Error != "" {
			return fmt.Errorf("docker pull error: %s", evt.Error)
		}
		if evt.ID != "" && evt.ProgressDetail.Total > 0 {
			layerCurrent[evt.ID] = evt.ProgressDetail.Current
			layerTotal[evt.ID] = evt.ProgressDetail.Total
		}

		var totalBytes, currentBytes int64
		for id, t := range layerTotal {
			totalBytes += t
			currentBytes += layerCurrent[id]
		}
		if totalBytes > 0 {
			const mb = 1024 * 1024
			pct := int(currentBytes * 100 / totalBytes)
			progressFn(float64(currentBytes)/float64(mb), float64(totalBytes)/float64(mb), pct)
		}
	}
	return scanner.Err() //nolint:wrapcheck // transparent scanner error
}

// ContainerLogs returns logs from a container.
func (c *Client) ContainerLogs(ctx context.Context, containerID string, tail int) (string, error) {
	reader, err := c.cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return "", err //nolint:wrapcheck // thin Docker SDK wrapper
	}
	defer reader.Close()

	result, err := demuxContainerLogs(reader)
	if err != nil {
		// Fallback: some containers use TTY mode (no multiplex headers)
		reader2, err2 := c.cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
			ShowStdout: true, ShowStderr: true, Tail: fmt.Sprintf("%d", tail),
		})
		if err2 != nil {
			return "", err2 //nolint:wrapcheck // thin Docker SDK wrapper
		}
		defer reader2.Close()
		data, _ := io.ReadAll(io.LimitReader(reader2, 50*1024*1024)) // 50MB cap
		return string(data), nil
	}
	return result, nil
}

// demuxContainerLogs demultiplexes a Docker multiplexed log stream into a
// single string, PRESERVING the chronological order in which frames were
// written. Both stdout and stderr are copied to ONE sink: stdcopy.StdCopy
// processes frames sequentially, so a single destination keeps each line in
// wire order. Copying stdout and stderr into separate buffers and
// concatenating them (all-stdout-then-all-stderr) reorders every stderr line
// to the very bottom regardless of timestamp — that made a container's startup
// stderr WARNINGs appear after runtime stdout lines hours newer, and made the
// visible tail change with the requested tail size (the "the end of the log
// differs per line count" bug). The live-follow path (handleAppLogsFollow)
// already writes both streams to one response writer for the same reason.
func demuxContainerLogs(reader io.Reader) (string, error) {
	var combined strings.Builder
	if _, err := stdcopy.StdCopy(&combined, &combined, reader); err != nil {
		return "", err //nolint:wrapcheck // thin Docker SDK wrapper
	}
	return combined.String(), nil
}

// demuxExecOutput demultiplexes a Docker exec attach stream into its two
// streams, kept SEPARATE (unlike demuxContainerLogs, whose readers want one
// chronological transcript). Each stream keeps its own write order; the
// relative order of the two is not recoverable from the frames and no caller
// needs it. A non-multiplexed (TTY-mode) stream has no frame headers and is
// reported as an error so the caller can fall back to reading it raw.
func demuxExecOutput(reader io.Reader) (stdout, stderr string, err error) {
	var out, errOut strings.Builder
	if _, err := stdcopy.StdCopy(&out, &errOut, reader); err != nil {
		return "", "", err //nolint:wrapcheck // thin Docker SDK wrapper
	}
	return out.String(), errOut.String(), nil
}

// ExecInContainer runs a command inside a running container and returns its
// output with stdout and stderr CONCATENATED (stdout first). Callers that must
// tell the two apart — anything parsing machine-readable output — want
// ExecInContainerSplit instead.
func (c *Client) ExecInContainer(ctx context.Context, containerID string, cmd []string) (output string, exitCode int, err error) {
	return execInContainerVia(ctx, c.ExecInContainerSplit, containerID, cmd)
}

// execSplitFn is the shape of ExecInContainerSplit.
type execSplitFn func(ctx context.Context, containerID string, cmd []string) (stdout, stderr string, exitCode int, err error)

// execInContainerVia is everything ExecInContainer does besides running the
// command: it appends stderr to stdout and passes the exit code and the error
// through untouched. It takes the split call as a parameter so the JOIN — an
// ORDER its callers depend on, since they scan one transcript for a marker —
// can be pinned without a live engine.
func execInContainerVia(ctx context.Context, split execSplitFn, containerID string, cmd []string) (output string, exitCode int, err error) {
	stdout, stderr, exitCode, err := split(ctx, containerID, cmd)
	return stdout + stderr, exitCode, err
}

// ExecInContainerSplit runs a command inside a running container and returns
// its two output streams separately, plus the exit code.
//
// err is reserved for a failure to run the command AT ALL (no such container,
// the daemon refused, the context expired): a command that ran and failed
// reports exitCode != 0 with a nil err, so a caller asking "did it work?" must
// look at the code and never at err alone.
//
// The split is what a dependency migration is defined over (migrate.Env.Exec):
// PostgreSQL's tools write results to stdout and diagnostics to stderr, so an
// inventory query must be parsed from stdout only — a concatenated stream would
// make a stray notice part of the answer — while a restore's errors are scanned
// on stderr, where psql prints them even on an exit code of 0.
func (c *Client) ExecInContainerSplit(ctx context.Context, containerID string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	execConfig := client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.cli.ExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", "", -1, err //nolint:wrapcheck // thin Docker SDK wrapper
	}

	attachResp, err := c.cli.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", "", -1, err //nolint:wrapcheck // thin Docker SDK wrapper
	}
	defer attachResp.Close()

	stdout, stderr, err = demuxExecOutput(attachResp.Reader)
	if err != nil {
		// Fallback for TTY-mode exec: the stream carries no frame headers, so
		// there is nothing to split — everything the exec wrote is reported as
		// stdout, which is also what the concatenating caller used to get.
		data, _ := io.ReadAll(attachResp.Reader)
		inspectResp, _ := c.cli.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
		return string(data), "", inspectResp.ExitCode, nil
	}

	// Get exit code
	inspectResp, err := c.cli.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return stdout, stderr, -1, err //nolint:wrapcheck // thin Docker SDK wrapper
	}

	return stdout, stderr, inspectResp.ExitCode, nil
}

// copiedFileMode is the mode every file the launcher copies into a container
// is written with: readable by whatever user the image runs as, and never
// executable.
const copiedFileMode = 0o644

// CopyFileToContainer writes one regular file into a running container.
//
// The Docker API takes a tar stream, so the tar header — not the API call —
// carries the mode and the ownership. Both are deliberate: uid/gid 0 (the
// default when CopyUIDGID is off) with mode 0644, so the file is readable by
// whatever user the image runs as (postgres 999, keycloak 1000, webapps root)
// and is NOT executable. The launcher's only user of this is the JVM attach
// class, and it must stay true that the launcher never drops an executable into
// a running production container.
func (c *Client) CopyFileToContainer(ctx context.Context, containerID, destDir, name string, data []byte) error {
	buf, err := tarSingleFile(name, data)
	if err != nil {
		return err
	}
	if _, err := c.cli.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{
		DestinationPath: destDir,
		Content:         buf,
	}); err != nil {
		return fmt.Errorf("copy %s into %s: %w", name, destDir, err)
	}
	return nil
}

// tarSingleFile builds the one-entry tar archive CopyFileToContainer sends.
func tarSingleFile(name string, data []byte) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     copiedFileMode,
		Size:     int64(len(data)),
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, fmt.Errorf("tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return nil, fmt.Errorf("tar body for %s: %w", name, err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("finish tar for %s: %w", name, err)
	}
	return &buf, nil
}

// GetPublishedPort returns the host port for a container's exposed port.
func (c *Client) GetPublishedPort(ctx context.Context, containerID string, containerPort int) int {
	inspect, err := c.InspectContainer(ctx, containerID)
	if err != nil {
		return -1
	}
	for port, bindings := range inspect.NetworkSettings.Ports {
		if int(port.Num()) == containerPort && len(bindings) > 0 {
			var hostPort int
			_, _ = fmt.Sscanf(bindings[0].HostPort, "%d", &hostPort)
			return hostPort
		}
	}
	return -1
}

// GetContainerIP returns the container's IP address on the namespace network.
// Returns empty string if the IP cannot be determined.
func (c *Client) GetContainerIP(ctx context.Context, containerID string) string {
	inspect, err := c.InspectContainer(ctx, containerID)
	if err != nil {
		return ""
	}
	// Prefer the namespace-specific network
	if ep, ok := inspect.NetworkSettings.Networks[c.NetworkName()]; ok && ep.IPAddress.IsValid() {
		return ep.IPAddress.String()
	}
	// Fallback: first non-empty IP from any network
	for _, ep := range inspect.NetworkSettings.Networks {
		if ep.IPAddress.IsValid() {
			return ep.IPAddress.String()
		}
	}
	return ""
}

// ContainerStats returns resource usage stats for a container.
//
// Uses the STREAMING stats endpoint (not one-shot): a one-shot read returns a
// zeroed precpu_stats, so the CPU% delta would be computed against zero and
// collapse to ~0.0% for every container. In streaming mode the second frame's
// precpu_stats is the first frame's cpu_stats — the same ~1s delta `docker
// stats` shows — so parseContainerStats reads the second frame. The 5s caller
// timeout bounds the (~1s) read.
func (c *Client) ContainerStats(ctx context.Context, containerID string) (*ContainerStat, error) {
	resp, err := c.cli.ContainerStats(ctx, containerID, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return nil, err //nolint:wrapcheck // thin Docker SDK wrapper
	}
	defer resp.Body.Close()
	return parseContainerStats(resp.Body)
}

// WaitForContainerExit waits for a container to finish and exit (for init containers).
// Uses Docker's ContainerWait API for instant notification instead of polling.
func (c *Client) WaitForContainerExit(ctx context.Context, containerID string, timeout time.Duration) error {
	// Pre-check: if container already exited, return immediately
	info, err := c.InspectContainer(ctx, containerID)
	if err == nil && !info.State.Running {
		if info.State.ExitCode != 0 {
			return fmt.Errorf("init container exited with code %d", info.State.ExitCode)
		}
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	wait := c.cli.ContainerWait(timeoutCtx, containerID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-timeoutCtx.Done():
		return fmt.Errorf("timeout waiting for container exit %s", containerID)
	case err := <-wait.Error:
		return fmt.Errorf("wait error for %s: %w", containerID, err)
	case result := <-wait.Result:
		if result.StatusCode != 0 {
			return fmt.Errorf("init container exited with code %d", result.StatusCode)
		}
		return nil
	}
}

// ParseMemory converts memory strings like "128m", "1g", "1.5g" to bytes.
// Fractional values are supported (e.g. "1.5g" = 1610612736), so sub-GiB
// precision can be written naturally instead of only in the smaller unit.
// Exported so the namespace generator can derive RabbitMQ's
// total_memory_available_override_value from the same limit string that fills
// HostConfig.Memory, keeping the two from ever drifting.
func ParseMemory(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}

	multiplier := float64(1)
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

	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	// Reject anything that can't be a sane byte count: parse errors, NaN/Inf,
	// negatives, and values that overflow int64. Float→int conversion of an
	// out-of-range value is implementation-defined in Go (minInt64 on amd64,
	// saturates on arm64), so guarding here keeps HostConfig.Memory / the
	// RabbitMQ override from ever seeing a garbage/negative byte count from a
	// user-typed `citeck edit` limit. All invalid input maps to 0 (no limit),
	// matching the historical empty/garbage behavior.
	v := n * multiplier
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v >= float64(math.MaxInt64) {
		return 0
	}
	return int64(v)
}

// ContainerStat holds resource usage for a container.
type ContainerStat struct {
	CPUPercent    float64
	CPUThrottled  bool
	MemUsage      int64
	MemLimit      int64
	MemoryPercent float64
}

// utilsRunTimeout is how long RunUtilsContainer waits for a utils command to
// finish. It is right for everything that call has ever run — `cat`, `du`,
// `df`, a `find` — all of which are seconds on any real volume.
//
// It is emphatically NOT right for a volume COPY, which is minutes to hours,
// and a copy killed at five minutes looks exactly like a copy that failed.
// That caller uses RunUtilsContainerWithTimeout with its own budget.
const utilsRunTimeout = 5 * time.Minute

// RunUtilsContainer runs a command in a temporary launcher-utils container
// with the given bind mounts, bounded by utilsRunTimeout.
func (c *Client) RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error) {
	return c.RunUtilsContainerWithTimeout(ctx, cmd, binds, utilsRunTimeout)
}

// RunUtilsContainerWithTimeout runs a command in a temporary launcher-utils
// container with the given bind mounts and an explicit wait budget.
// It creates the container, starts it, waits for exit, captures output, and removes it.
//
// err is reserved for a failure to RUN the command — the create, the start,
// the wait, or an inspect that would not say how the container exited (see
// utilsExitCode). A command that ran and failed reports its own exit code with
// a nil err, which is the distinction every caller is written against.
//
// The timeout is the caller's because the callers are not alike: the seconds
// a `cat` takes and the hours a volume copy takes cannot share one number, and
// a copy cut short at the `cat`'s budget is indistinguishable from a copy that
// failed — on a namespace the migration has already stopped.
func (c *Client) RunUtilsContainerWithTimeout(ctx context.Context, cmd, binds []string,
	timeout time.Duration,
) (output string, exitCode int, err error) {
	utilsImage := config.UtilsImage()

	containerName := c.ContainerName("launcher-utils-tmp")
	_ = c.StopAndRemoveContainer(ctx, containerName, 0)

	resp, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: utilsImage,
			Cmd:   cmd,
			Labels: map[string]string{
				LabelLauncher:  "true",
				LabelWorkspace: c.workspace,
				LabelNamespace: c.namespace,
				LabelAppName:   "launcher-utils",
			},
		},
		HostConfig: &container.HostConfig{
			Binds: binds,
		},
		Name: containerName,
	})
	if err != nil {
		return "", -1, fmt.Errorf("create utils container: %w", err)
	}
	containerID := resp.ID
	defer func() { _ = c.RemoveContainer(ctx, containerID) }()

	if _, startErr := c.cli.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); startErr != nil {
		return "", -1, fmt.Errorf("start utils container: %w", startErr)
	}

	if waitErr := c.WaitForContainerExit(ctx, containerID, timeout); waitErr != nil {
		return "", -1, fmt.Errorf("wait for utils container: %w", waitErr)
	}

	output, _ = c.ContainerLogs(ctx, containerID, 1000)

	exitCode, err = utilsExitCode(c.InspectContainer(ctx, containerID))
	return output, exitCode, err
}

// utilsExitCode reads a finished utils container's exit code out of an inspect
// answer, and reports the two ways that can fail as ERRORS rather than as an
// exit code of -1.
//
// The distinction is the whole contract of RunUtilsContainer: err means the
// command could not be RUN, an exit code means it ran and failed. Answering a
// failed inspect with (-1, nil) blurred the two — and its callers are built on
// keeping them apart. The dependency-pin seeding probe reads PG_VERSION out of
// a volume through this call and its verdict is deliberately tri-state (a probe
// FAILURE pins the legacy image, a successful "there is nothing here" writes no
// pin), so an engine that would not say how the `cat` exited must not be
// reported as the `cat` having failed. The migration Env states the same rule
// for ContainerRunning: an absent container is an ANSWER, a failed inspect is
// not.
//
// A response with no State block is the second shape, and used to be worse than
// wrong: InspectResponse.State is a pointer, so reading ExitCode off it
// panicked inside the daemon.
func utilsExitCode(inspect container.InspectResponse, inspErr error) (int, error) {
	if inspErr != nil {
		return -1, fmt.Errorf("inspect utils container: %w", inspErr)
	}
	if inspect.State == nil {
		return -1, errors.New("inspect utils container: the engine reported no container state")
	}
	return inspect.State.ExitCode, nil
}
