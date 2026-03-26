package namespace

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
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
)

var (
	jwtSecretOnce sync.Once
	jwtSecretVal  string

	oidcSecretOnce sync.Once
	oidcSecretVal  string
)

// JWTSecret returns a stable JWT secret, generated once and persisted to disk.
// This avoids hardcoding a well-known secret while ensuring all webapps share the same key.
func JWTSecret() string {
	jwtSecretOnce.Do(func() {
		secretPath := filepath.Join(config.ConfDir(), "jwt-secret")
		if data, err := os.ReadFile(secretPath); err == nil && len(data) > 0 {
			jwtSecretVal = string(data)
			return
		}
		// Generate 64-byte random secret, base64-encoded (~86 chars = 688 bits, HS512 needs >= 512)
		b := make([]byte, 64)
		if _, err := rand.Read(b); err != nil {
			slog.Error("Failed to generate JWT secret", "err", err)
			return
		}
		jwtSecretVal = base64.RawURLEncoding.EncodeToString(b)
		os.MkdirAll(filepath.Dir(secretPath), 0o755)
		fsutil.AtomicWriteFile(secretPath, []byte(jwtSecretVal), 0o600)
	})
	return jwtSecretVal
}

// OIDCSecret returns a stable OIDC client secret, generated once and persisted to disk.
// Replaces the hardcoded UUID that was identical across all deployments.
func OIDCSecret() string {
	oidcSecretOnce.Do(func() {
		secretPath := filepath.Join(config.ConfDir(), "oidc-secret")
		if data, err := os.ReadFile(secretPath); err == nil && len(data) > 0 {
			oidcSecretVal = string(data)
			return
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			slog.Error("Failed to generate OIDC secret", "err", err)
			return
		}
		oidcSecretVal = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		os.MkdirAll(filepath.Dir(secretPath), 0o755)
		fsutil.AtomicWriteFile(secretPath, []byte(oidcSecretVal), 0o600)
	})
	return oidcSecretVal
}

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

// IsLocalHost returns true if the proxy host is localhost/127.0.0.1/::1 or empty.
func (c *NsGenContext) IsLocalHost() bool {
	h := c.Config.Proxy.Host
	return h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ProxyScheme returns "https" or "http".
// For non-local hosts, always returns "https" (assumed behind reverse proxy).
func (c *NsGenContext) ProxyScheme() string {
	if c.TLSEnabled() || !c.IsLocalHost() {
		return "https"
	}
	return "http"
}

// ProxyBaseURL returns the full base URL for the proxy (includes port if non-standard).
func (c *NsGenContext) ProxyBaseURL() string {
	return BuildProxyBaseURL(c.Config.Proxy)
}

// BuildProxyBaseURL builds a proxy base URL from proxy config.
// For non-local hosts (not localhost/127.0.0.1), always uses https scheme
// even when TLS is disabled locally — the server is assumed to be behind
// a reverse proxy (Cloudflare, nginx) that terminates TLS.
func BuildProxyBaseURL(p ProxyProps) string {
	host := p.Host
	if host == "" {
		host = "localhost"
	}

	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"

	scheme := "http"
	if p.TLS.Enabled || !isLocal {
		scheme = "https"
	}

	port := p.Port

	defaultPort := 80
	if scheme == "https" {
		defaultPort = 443
	}
	// For external hosts without local TLS, port 80 is the local listen port
	// but clients connect via reverse proxy on 443 — omit port from URL.
	if !isLocal && !p.TLS.Enabled && port == 80 {
		return fmt.Sprintf("%s://%s", scheme, host)
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
