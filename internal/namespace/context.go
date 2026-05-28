package namespace

import (
	"fmt"
	"net"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Infrastructure host/port constants.
const (
	KKHost = "keycloak"
	// KeycloakDBName is the postgres database name used by keycloak. Also
	// re-used as both the DB username and DB password (low-stakes — only
	// reachable from inside the docker network; never exposed externally).
	// Referenced by both the keycloak container spec (Cmd args) and the
	// admin-password recovery path that needs to invoke `kc.sh
	// bootstrap-admin user` with explicit DB connection parameters.
	// Use KeycloakDBJDBCURL() to get the full JDBC URL.
	KeycloakDBName = "citeck_keycloak"
	PGHost         = "postgres"
	PGPort         = 5432
	ZKHost         = "zookeeper"
	ZKPort         = 2181
	RMQHost        = "rabbitmq"
	RMQPort        = 5672
	MongoHost      = "mongo"
	MongoPort      = 27017
	MailhogHost    = "mailhog"
	OnlyofficeHost = "onlyoffice"
	ObsPGHost      = "observer-postgres"
)

// CiteckSAUser is the canonical service-account username. The same name is
// used for the Keycloak master-realm SA (admin role) and the RabbitMQ user
// (monitoring tag, vhost "/" full perms). Webapps authenticate to both
// systems using this identity so that admin-password rotations never
// require webapp container recreation.
const CiteckSAUser = "citeck"

// LegacyCiteckSAUser is the pre-rename SA username. The Keycloak init
// script deletes it on startup to keep the master realm clean after upgrade.
const LegacyCiteckSAUser = "citeck-launcher"

// KeycloakDBJDBCURL returns the JDBC URL the keycloak container (and the
// bootstrap-admin recovery path in the daemon) use to talk to postgres.
// Centralized so the formula lives in one place — generator's Cmd args
// and the recovery helper otherwise drift independently.
func KeycloakDBJDBCURL() string {
	return fmt.Sprintf("jdbc:postgresql://%s:%d/%s", PGHost, PGPort, KeycloakDBName)
}

// SystemSecrets holds system-managed secrets resolved at daemon startup.
// AdminPassword is the plaintext password for the ecos-app realm admin user;
// the generator hashes it into the realm.json credential before the keycloak
// container imports the realm on first start.
// CiteckSA is a dedicated service account password for the "citeck" user.
// The same password is used for two stable identities:
//   - Keycloak master realm (admin role) — for all kcadm operations.
//   - RabbitMQ (monitoring tag, vhost "/" full perms) — used by webapps
//     instead of the user-facing admin user so that admin-password changes
//     no longer require recreating webapp containers.
//
// Because this password is stable across admin-password changes and snapshot
// imports, it can be baked into webapp container env vars without causing
// restarts when the admin password rotates.
type SystemSecrets struct {
	JWT           string
	OIDC          string
	AdminPassword string
	CiteckSA      string
}

// AdminPasswordOrDefault returns the generated admin password, or "admin"
// when secrets aren't populated (unit tests, first-ever generate before
// the daemon has generated the system secret).
func (s SystemSecrets) AdminPasswordOrDefault() string {
	if s.AdminPassword != "" {
		return s.AdminPassword
	}
	return "admin"
}

// NsGenContext holds state during namespace generation.
type NsGenContext struct {
	Config          *Config
	Bundle          *bundle.Def
	WorkspaceConfig *bundle.WorkspaceConfig
	Secrets         SystemSecrets
	SecretReader    SecretReader // resolves "secret:" references in config
	DetachedApps    map[string]bool
	Files           map[string][]byte
	Applications    map[string]*AppBuilder
	CloudConfig     map[string]map[string]any // per-app ext cloud config (for CloudConfigServer on :8761)
	// ExtraLicenses are user-added enterprise licenses stored encrypted via the
	// license.Service. Merged with WorkspaceConfig.Licenses in the eapps cloud
	// config so UI-added licenses actually reach the running webapps.
	ExtraLicenses []bundle.LicenseInstance
	// EditedFileOverlay is the user-edited disk content keyed by canonical
	// ctx.Files key. The generator overlays it into the hash input only (not
	// into ctx.Files itself) so deployment-hash recompute drives container
	// recreate after a Web UI file edit.
	EditedFileOverlay map[string][]byte
	portsCounter      atomic.Int32
}

// NewNsGenContext creates a new generation context for the given config and bundle.
func NewNsGenContext(cfg *Config, bun *bundle.Def) *NsGenContext {
	ctx := &NsGenContext{
		Config:       cfg,
		Bundle:       bun,
		DetachedApps: make(map[string]bool),
		Files:        make(map[string][]byte),
		Applications: make(map[string]*AppBuilder),
		CloudConfig:  make(map[string]map[string]any),
	}
	ctx.portsCounter.Store(17020)
	return ctx
}

// NextPort atomically returns the current port value and increments for next call.
// Equivalent to Kotlin's AtomicInteger.getAndIncrement().
func (c *NsGenContext) NextPort() int {
	return int(c.portsCounter.Add(1) - 1)
}

// GetOrCreateApp returns an existing AppBuilder or creates a new one for the given name.
func (c *NsGenContext) GetOrCreateApp(name string) *AppBuilder {
	if b, ok := c.Applications[name]; ok {
		return b
	}
	b := &AppBuilder{
		Name:         name,
		Environments: make(map[string]string),
		DependsOn:    make(map[string]bool),
	}
	c.Applications[name] = b
	return b
}

// ProxyHost returns the configured proxy host or "localhost" if blank.
func (c *NsGenContext) ProxyHost() string {
	h := c.Config.Proxy.Host
	if h == "" {
		return "localhost"
	}
	return h
}

// TLSEnabled returns whether TLS is enabled.
func (c *NsGenContext) TLSEnabled() bool {
	return c.Config.Proxy.TLS.Enabled
}

// IsLocalHost returns true if the proxy host is localhost/127.0.0.1/::1 or empty.
func (c *NsGenContext) IsLocalHost() bool {
	h := c.Config.Proxy.Host
	return h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// PresumedHTTPS reports whether clients reach the platform over HTTPS, either
// because local TLS is on, or because a reverse proxy (Cloudflare / nginx)
// terminates TLS in front of a domain host. Raw IP hosts are never assumed
// to sit behind such a proxy.
func (c *NsGenContext) PresumedHTTPS() bool {
	if c.TLSEnabled() {
		return true
	}
	if c.IsLocalHost() {
		return false
	}
	return net.ParseIP(c.Config.Proxy.Host) == nil
}

// ProxyScheme returns "https" or "http".
// For non-local domain hosts, returns "https" (assumed behind reverse proxy
// terminating TLS). Raw IP hosts with TLS disabled get "http" so OIDC
// redirect URIs match what the browser can actually reach — a stale https://
// here makes Keycloak reject the redirect_uri with "Invalid parameter".
func (c *NsGenContext) ProxyScheme() string {
	if c.PresumedHTTPS() {
		return "https"
	}
	return "http"
}

// ProxyBaseURL returns the full base URL for the proxy (includes port if non-standard).
func (c *NsGenContext) ProxyBaseURL() string {
	return BuildProxyBaseURL(c.Config.Proxy)
}

// BuildProxyBaseURL builds a proxy base URL from proxy config.
// For non-local domain hosts with TLS disabled, assumes a reverse proxy
// (Cloudflare, nginx) terminates TLS and uses https without port. For raw
// IP hosts with TLS disabled, there's no plausible reverse proxy in front
// — emit http://ip:port directly so the printed URL actually works.
func BuildProxyBaseURL(p ProxyProps) string {
	host := p.Host
	if host == "" {
		host = "localhost"
	}

	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	isIP := net.ParseIP(host) != nil

	scheme := "http"
	if p.TLS.Enabled || (!isLocal && !isIP) {
		scheme = "https"
	}

	port := p.Port

	defaultPort := 80
	if scheme == "https" {
		defaultPort = 443
	}
	// For external domain hosts without local TLS, port 80 is the local
	// listen port but clients connect via reverse proxy on 443 — omit port
	// from URL. Raw IP hosts fall through to the explicit port branch.
	if !isLocal && !isIP && !p.TLS.Enabled && port == 80 {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	if port == 0 || port == defaultPort {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// AppBuilder accumulates ApplicationDef properties during generation.
type AppBuilder struct {
	Name               string
	NetworkAliases     []string
	Image              string
	Environments       map[string]string
	Cmd                []string
	Ports              []string
	Volumes            []string
	VolumesContentHash string
	InitActions        []appdef.AppInitAction
	DependsOn          map[string]bool
	StartupConditions  []appdef.StartupCondition
	LivenessProbe      *appdef.AppProbeDef
	Resources          *appdef.AppResourcesDef
	Kind               appdef.ApplicationKind
	ShmSize            string
	InitContainers     []appdef.InitContainerDef
}

// AddEnv sets an environment variable for the app.
func (b *AppBuilder) AddEnv(key, value string) *AppBuilder {
	b.Environments[key] = value
	return b
}

// AddPort adds a port mapping to the app.
func (b *AppBuilder) AddPort(port string) *AppBuilder {
	b.Ports = append(b.Ports, port)
	return b
}

// AddVolume adds a volume mount to the app.
func (b *AppBuilder) AddVolume(volume string) *AppBuilder {
	b.Volumes = append(b.Volumes, volume)
	return b
}

// AddDependsOn adds a dependency on another app.
func (b *AppBuilder) AddDependsOn(name string) *AppBuilder {
	b.DependsOn[name] = true
	return b
}

// Build converts the builder into an immutable ApplicationDef.
func (b *AppBuilder) Build() appdef.ApplicationDef {
	return appdef.ApplicationDef{
		Name:               b.Name,
		NetworkAliases:     b.NetworkAliases,
		Image:              b.Image,
		Environments:       b.Environments,
		Cmd:                b.Cmd,
		Ports:              b.Ports,
		Volumes:            b.Volumes,
		VolumesContentHash: b.VolumesContentHash,
		InitActions:        b.InitActions,
		DependsOn:          b.DependsOn,
		StartupConditions:  b.StartupConditions,
		LivenessProbe:      b.LivenessProbe,
		Resources:          b.Resources,
		Kind:               b.Kind,
		ShmSize:            b.ShmSize,
		InitContainers:     b.InitContainers,
	}
}
