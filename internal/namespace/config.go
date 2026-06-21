package namespace

import (
	"bytes"
	"fmt"
	"os"

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
	// PublishAllPorts, in server mode, publishes EVERY app's host ports (desktop
	// parity) instead of only the proxy's. This lets an external microservice on
	// the host (e.g. citeck-uni carrying migration backend logic) join the stack
	// by reaching zookeeper/rabbitmq/postgres/webapp ports on localhost. No effect
	// in desktop mode (which already publishes all ports).
	PublishAllPorts bool `yaml:"publishAllPorts,omitempty" json:"publishAllPorts,omitempty"`
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
	return nil
}
