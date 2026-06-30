package namespace

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"gopkg.in/yaml.v3"
)

// AuthenticationType defines the authentication mode for a namespace.
type AuthenticationType string

// Authentication modes.
const (
	AuthBasic    AuthenticationType = "BASIC"
	AuthKeycloak AuthenticationType = "KEYCLOAK"
)

// TlsConfig holds TLS settings for the proxy.
type TlsConfig struct { //nolint:revive // TLS acronym casing matches YAML config field naming
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	CertPath    string `yaml:"certPath" json:"certPath"`
	KeyPath     string `yaml:"keyPath" json:"keyPath"`
	LetsEncrypt bool   `yaml:"letsEncrypt" json:"letsEncrypt"`
}

// ProxyProps holds proxy configuration for a namespace.
type ProxyProps struct {
	Image string    `yaml:"image" json:"image"`
	Port  int       `yaml:"port" json:"port"`
	Host  string    `yaml:"host" json:"host"`
	TLS   TlsConfig `yaml:"tls" json:"tls"`
}

// AuthenticationProps holds authentication settings.
type AuthenticationProps struct {
	Type  AuthenticationType `yaml:"type" json:"type"`
	Users []string           `yaml:"users" json:"users"`
}

// PgAdminProps holds pgAdmin settings.
type PgAdminProps struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Image   string `yaml:"image" json:"image"`
}

// MongoDbProps holds MongoDB image configuration.
type MongoDbProps struct {
	Image string `yaml:"image" json:"image"`
}

// ObserverProps holds citeck-observer settings.
type ObserverProps struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Image   string `yaml:"image" json:"image"`
}

// AdditionalAppProps declares a custom container to run in the namespace alongside
// the built-in Citeck/infra apps — so a new service (e.g. a mock/simulator or any
// auxiliary Go service) can be added by configuration alone, without a dedicated
// generator in the launcher. It mirrors the generic appdef.ApplicationDef fields;
// environment values support the same ${PG_HOST}/${ZK_HOST}/… template variables as
// the rest of the config. In server mode the container is internal to the Docker
// network (reachable by Name/NetworkAliases) — only the proxy publishes ports.
type AdditionalAppProps struct {
	// Name is the container/app name (unique; must not collide with a built-in app).
	Name string `yaml:"name" json:"name"`
	// Enabled defaults to true; set false to keep the definition but not deploy it.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Image is the full Docker image reference to run (registry/repo:tag, or a
	// locally-present tag). Pulled like any other app; registry auth comes from the
	// workspace imageRepos by host match.
	Image string `yaml:"image" json:"image"`
	// Kind classifies the app (CITECK_CORE / CITECK_CORE_EXTENSION / CITECK_ADDITIONAL
	// / THIRD_PARTY); empty defaults to THIRD_PARTY.
	Kind              string                    `yaml:"kind,omitempty" json:"kind,omitempty"`
	NetworkAliases    []string                  `yaml:"networkAliases,omitempty" json:"networkAliases,omitempty"`
	Environments      map[string]string         `yaml:"environments,omitempty" json:"environments,omitempty"`
	Cmd               []string                  `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	Ports             []string                  `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes           []string                  `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	DependsOn         []string                  `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	StartupConditions []appdef.StartupCondition `yaml:"startupConditions,omitempty" json:"startupConditions,omitempty"`
	LivenessProbe     *appdef.AppProbeDef       `yaml:"livenessProbe,omitempty" json:"livenessProbe,omitempty"`
	Resources         *appdef.AppResourcesDef   `yaml:"resources,omitempty" json:"resources,omitempty"`
	ShmSize           string                    `yaml:"shmSize,omitempty" json:"shmSize,omitempty"`
	// InitContainers run to completion before the main container starts (a
	// wait-for, a schema migration, a fixture loader). Each is a full
	// InitContainerDef (image + env + volumes + cmd); ${VAR} is resolved in env
	// and cmd just like the main container.
	InitContainers []appdef.InitContainerDef `yaml:"initContainers,omitempty" json:"initContainers,omitempty"`
	// InitActions are exec commands run inside the container right after it is
	// created (e.g. createbucket, a one-off CLI call). ${VAR} is resolved in args.
	InitActions []appdef.AppInitAction `yaml:"initActions,omitempty" json:"initActions,omitempty"`
	// StopTimeout is the per-app graceful-stop budget in seconds (SIGTERM→SIGKILL
	// window); 0 falls back to the daemon default.
	StopTimeout int `yaml:"stopTimeout,omitempty" json:"stopTimeout,omitempty"`
}

// IsEnabled reports whether the additional app should be deployed (default true).
func (a AdditionalAppProps) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// WebappProps holds per-webapp overrides in namespace config.
type WebappProps struct {
	Enabled          *bool                              `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Image            string                             `yaml:"image" json:"image"`
	Environments     map[string]string                  `yaml:"environments,omitempty" json:"environments,omitempty"`
	CloudConfig      map[string]any                     `yaml:"cloudConfig,omitempty" json:"cloudConfig,omitempty"`
	DataSources      map[string]bundle.DataSourceConfig `yaml:"dataSources,omitempty" json:"dataSources,omitempty"`
	DebugPort        int                                `yaml:"debugPort" json:"debugPort"`
	HeapSize         string                             `yaml:"heapSize" json:"heapSize"`
	MemoryLimit      string                             `yaml:"memoryLimit" json:"memoryLimit"`
	ServerPort       int                                `yaml:"serverPort" json:"serverPort"`
	JavaOpts         string                             `yaml:"javaOpts" json:"javaOpts"`
	SpringProfiles   string                             `yaml:"springProfiles" json:"springProfiles"`
	LivenessDisabled bool                               `yaml:"livenessDisabled,omitempty" json:"livenessDisabled,omitempty"`
}

// EmailConfig configures external SMTP. When set, mailpit is not generated.
// Password field uses "secret:<key>" reference to SecretService.
type EmailConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
	From     string `yaml:"from" json:"from"`
	TLS      bool   `yaml:"tls" json:"tls"`
	// StartupNotification asks the notifications microservice to send a test
	// email when it starts up. Maps to ecos-notifications.startup-notification.*
	// env vars. Nil when disabled so upgrades from older configs don't grow
	// a noisy empty block.
	StartupNotification *StartupNotificationConfig `yaml:"startupNotification,omitempty" json:"startupNotification,omitempty"`
}

// StartupNotificationConfig controls the notifications-service startup
// probe email. Created only when the user opts in through the setup wizard.
type StartupNotificationConfig struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Recipient string `yaml:"recipient,omitempty" json:"recipient,omitempty"`
}

// S3Config configures S3 storage for ecos-content.
// SecretKey field uses "secret:<key>" reference to SecretService.
type S3Config struct {
	Endpoint  string `yaml:"endpoint" json:"endpoint"`
	Bucket    string `yaml:"bucket" json:"bucket"`
	AccessKey string `yaml:"accessKey" json:"accessKey"`
	SecretKey string `yaml:"secretKey" json:"secretKey"`
	Region    string `yaml:"region,omitempty" json:"region,omitempty"`
}

// Config is the top-level namespace configuration (namespace.yml).
type Config struct {
	APIVersion     string                 `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`
	ID             string                 `yaml:"id" json:"id"`
	Name           string                 `yaml:"name" json:"name"`
	Snapshot       string                 `yaml:"snapshot" json:"snapshot"`
	Template       string                 `yaml:"template" json:"template"`
	Authentication AuthenticationProps    `yaml:"authentication" json:"authentication"`
	BundleRef      bundle.Ref             `yaml:"bundleRef" json:"bundleRef"`
	PgAdmin        PgAdminProps           `yaml:"pgAdmin" json:"pgAdmin"`
	MongoDB        MongoDbProps           `yaml:"mongodb" json:"mongodb"`
	Proxy          ProxyProps             `yaml:"proxy" json:"proxy"`
	Observer       ObserverProps          `yaml:"observer" json:"observer"`
	Webapps        map[string]WebappProps `yaml:"webapps,omitempty" json:"webapps,omitempty"`
	Email          *EmailConfig           `yaml:"email,omitempty" json:"email,omitempty"`
	S3             *S3Config              `yaml:"s3,omitempty" json:"s3,omitempty"`
	// AdditionalApps are custom containers added by configuration alone (no
	// dedicated launcher generator). See AdditionalAppProps.
	AdditionalApps []AdditionalAppProps `yaml:"additionalApps,omitempty" json:"additionalApps,omitempty"`
}

// DefaultNamespaceConfig returns a namespace config with sensible defaults.
func DefaultNamespaceConfig() Config {
	return Config{
		Authentication: AuthenticationProps{
			Type: AuthBasic,
			// Kotlin AuthenticationProps.DEFAULT = setOf("admin", "fet").
			Users: []string{"admin", "fet"},
		},
		PgAdmin: PgAdminProps{Enabled: true},
		Proxy:   ProxyProps{Port: 80},
	}
}

// LoadNamespaceConfig reads and parses a namespace config from the given file path.
func LoadNamespaceConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is from trusted config directory
	if err != nil {
		return nil, fmt.Errorf("read namespace config: %w", err)
	}
	return ParseNamespaceConfig(data)
}

// MarshalNamespaceConfig serializes a namespace config to YAML with a 2-space
// indent (matching the Kotlin 1.x launcher and the editor's display width).
func MarshalNamespaceConfig(cfg *Config) ([]byte, error) {
	out := *cfg
	if out.APIVersion == "" {
		out.APIVersion = "v1"
	}
	return marshalYAML2(&out)
}

// marshalYAML2 marshals v to YAML using a 2-space indent. yaml.v3's package
// Marshal hardcodes a 4-space indent, so we drive an Encoder directly.
func marshalYAML2(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// ParseNamespaceConfig parses YAML data into a namespace config, applying defaults and validation.
func ParseNamespaceConfig(data []byte) (*Config, error) {
	cfg := DefaultNamespaceConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse namespace config: %w", err)
	}
	if cfg.ID == "" {
		cfg.ID = "default"
	}
	if err := ValidateNamespaceConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ValidateYAML parses raw namespace-config YAML and runs full business-rule
// validation, returning the parsed Config. It is the single validation gate
// used before any config is written to the store — on the daemon's write
// paths and in the H2→SQLite migration alike. Validating the exact bytes that
// will be stored catches both bad input and a serializer defect.
func ValidateYAML(raw []byte) (*Config, error) {
	return ParseNamespaceConfig(raw)
}

// ValidateNamespaceConfig checks namespace config for common errors.
func ValidateNamespaceConfig(cfg *Config) error {
	if cfg.Proxy.Port < 1 || cfg.Proxy.Port > 65535 {
		return fmt.Errorf("proxy port must be 1-65535, got %d", cfg.Proxy.Port)
	}
	if cfg.Proxy.TLS.Enabled && cfg.Proxy.Host == "" {
		return fmt.Errorf("proxy host required when TLS is enabled")
	}
	if cfg.Proxy.TLS.LetsEncrypt {
		host := cfg.Proxy.Host
		if host == "" || host == "localhost" || host == "127.0.0.1" {
			return fmt.Errorf("let's encrypt requires a public hostname, got %q", host)
		}
	}
	if cfg.Authentication.Type == AuthBasic && len(cfg.Authentication.Users) == 0 {
		return fmt.Errorf("at least one user required for BASIC authentication")
	}
	if cfg.Email != nil {
		if cfg.Email.Host == "" {
			return fmt.Errorf("email host is required")
		}
		if cfg.Email.Port < 1 || cfg.Email.Port > 65535 {
			return fmt.Errorf("email port must be 1-65535, got %d", cfg.Email.Port)
		}
		if cfg.Email.From == "" {
			return fmt.Errorf("email from address is required")
		}
	}
	if cfg.S3 != nil {
		if cfg.S3.Endpoint == "" {
			return fmt.Errorf("s3 endpoint is required")
		}
		if cfg.S3.Bucket == "" {
			return fmt.Errorf("s3 bucket is required")
		}
		if cfg.S3.AccessKey == "" {
			return fmt.Errorf("s3 access key is required")
		}
		if cfg.S3.SecretKey == "" {
			return fmt.Errorf("s3 secret key is required")
		}
	}
	if err := validateAdditionalApps(cfg.AdditionalApps); err != nil {
		return err
	}
	return nil
}

// reservedAppNames are the built-in infra/core container names an additional app
// must not reuse (reusing one would override that built-in app's definition).
var reservedAppNames = map[string]bool{
	appdef.AppProxy: true, appdef.AppGateway: true, appdef.AppEapps: true,
	appdef.AppEmodel: true, appdef.AppUiserv: true, appdef.AppHistory: true,
	appdef.AppNotifications: true, appdef.AppTransformations: true, appdef.AppEproc: true,
	appdef.AppPostgres: true, appdef.AppZookeeper: true, appdef.AppRabbitmq: true,
	appdef.AppMongodb: true, appdef.AppMailpit: true, appdef.AppKeycloak: true,
	appdef.AppPgadmin: true, appdef.AppOnlyoffice: true, appdef.AppAlfresco: true,
	appdef.AppAlfPostgres: true, appdef.AppAlfSolr: true, appdef.AppObserver: true,
	appdef.AppObsPostgres: true, appdef.AppContent: true, appdef.AppAi: true,
	appdef.AppSttSidecar: true,
}

// validateAdditionalApps checks each additional app has a name + image, names are
// unique, and they do not collide with a reserved built-in container name.
func validateAdditionalApps(apps []AdditionalAppProps) error {
	seen := make(map[string]bool, len(apps))
	for i, a := range apps {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return fmt.Errorf("additionalApps[%d]: name is required", i)
		}
		if strings.TrimSpace(a.Image) == "" {
			return fmt.Errorf("additionalApps[%q]: image is required", name)
		}
		if reservedAppNames[name] {
			return fmt.Errorf("additionalApps[%q]: name collides with a built-in app; choose another", name)
		}
		if seen[name] {
			return fmt.Errorf("additionalApps[%q]: duplicate name", name)
		}
		seen[name] = true
		for j, ic := range a.InitContainers {
			if strings.TrimSpace(ic.Image) == "" {
				return fmt.Errorf("additionalApps[%q].initContainers[%d]: image is required", name, j)
			}
		}
		if a.StopTimeout < 0 {
			return fmt.Errorf("additionalApps[%q]: stopTimeout must be >= 0", name)
		}
	}
	return nil
}
