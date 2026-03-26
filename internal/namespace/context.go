package namespace

import (
	"fmt"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Infrastructure host/port constants.
const (
	KKHost         = "keycloak"
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
	JWTSecret      = "my-secret-key-which-should-be-changed-in-production-and-be-base64-encoded"
)

// NsGenContext holds state during namespace generation.
type NsGenContext struct {
	Config          *NamespaceConfig
	Bundle          *bundle.BundleDef
	WorkspaceConfig *bundle.WorkspaceConfig
	DetachedApps    map[string]bool
	Files           map[string][]byte
	Applications    map[string]*AppBuilder
	CloudConfig     map[string]map[string]any // per-app ext cloud config (for CloudConfigServer on :8761)
	portsCounter    atomic.Int32
}

func NewNsGenContext(cfg *NamespaceConfig, bun *bundle.BundleDef) *NsGenContext {
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

// ProxyScheme returns "https" or "http".
func (c *NsGenContext) ProxyScheme() string {
	if c.TLSEnabled() {
		return "https"
	}
	return "http"
}

// ProxyBaseURL returns the full base URL for the proxy (includes port if non-standard).
func (c *NsGenContext) ProxyBaseURL() string {
	return BuildProxyBaseURL(c.Config.Proxy)
}

// BuildProxyBaseURL builds a proxy base URL from proxy config.
func BuildProxyBaseURL(p ProxyProps) string {
	scheme := "http"
	if p.TLS.Enabled {
		scheme = "https"
	}
	host := p.Host
	if host == "" {
		host = "localhost"
	}
	port := p.Port
	defaultPort := 80
	if p.TLS.Enabled {
		defaultPort = 443
	}
	if port == 0 || port == defaultPort {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// AppBuilder accumulates ApplicationDef properties during generation.
type AppBuilder struct {
	Name              string
	NetworkAliases    []string
	Image             string
	Environments      map[string]string
	Cmd               []string
	Ports             []string
	Volumes           []string
	VolumesContentHash string
	InitActions       []appdef.AppInitAction
	DependsOn         map[string]bool
	StartupConditions []appdef.StartupCondition
	LivenessProbe     *appdef.AppProbeDef
	Resources         *appdef.AppResourcesDef
	Kind              appdef.ApplicationKind
	ShmSize           string
	InitContainers    []appdef.InitContainerDef
}

func (b *AppBuilder) AddEnv(key, value string) *AppBuilder {
	b.Environments[key] = value
	return b
}

func (b *AppBuilder) AddPort(port string) *AppBuilder {
	b.Ports = append(b.Ports, port)
	return b
}

func (b *AppBuilder) AddVolume(volume string) *AppBuilder {
	b.Volumes = append(b.Volumes, volume)
	return b
}

func (b *AppBuilder) AddDependsOn(name string) *AppBuilder {
	b.DependsOn[name] = true
	return b
}

func (b *AppBuilder) Build() appdef.ApplicationDef {
	return appdef.ApplicationDef{
		Name:              b.Name,
		NetworkAliases:    b.NetworkAliases,
		Image:             b.Image,
		Environments:      b.Environments,
		Cmd:               b.Cmd,
		Ports:             b.Ports,
		Volumes:           b.Volumes,
		VolumesContentHash: b.VolumesContentHash,
		InitActions:       b.InitActions,
		DependsOn:         b.DependsOn,
		StartupConditions: b.StartupConditions,
		LivenessProbe:     b.LivenessProbe,
		Resources:         b.Resources,
		Kind:              b.Kind,
		ShmSize:           b.ShmSize,
		InitContainers:    b.InitContainers,
	}
}
