package namespace

import (
	"fmt"
	"os"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"gopkg.in/yaml.v3"
)

type AuthenticationType string

const (
	AuthBasic    AuthenticationType = "BASIC"
	AuthKeycloak AuthenticationType = "KEYCLOAK"
)

type TlsConfig struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	CertPath    string `yaml:"certPath" json:"certPath"`
	KeyPath     string `yaml:"keyPath" json:"keyPath"`
	LetsEncrypt bool   `yaml:"letsEncrypt" json:"letsEncrypt"`
}

type ProxyProps struct {
	Image string    `yaml:"image" json:"image"`
	Port  int       `yaml:"port" json:"port"`
	Host  string    `yaml:"host" json:"host"`
	TLS   TlsConfig `yaml:"tls" json:"tls"`
}

type AuthenticationProps struct {
	Type  AuthenticationType `yaml:"type" json:"type"`
	Users []string           `yaml:"users" json:"users"`
}

type PgAdminProps struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Image   string `yaml:"image" json:"image"`
}

type MongoDbProps struct {
	Image string `yaml:"image" json:"image"`
}

type WebappProps struct {
	Enabled        *bool                                `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Image          string                               `yaml:"image" json:"image"`
	Environments   map[string]string                    `yaml:"environments,omitempty" json:"environments,omitempty"`
	CloudConfig    map[string]any                       `yaml:"cloudConfig,omitempty" json:"cloudConfig,omitempty"`
	DataSources    map[string]bundle.DataSourceConfig   `yaml:"dataSources,omitempty" json:"dataSources,omitempty"`
	DebugPort      int                                  `yaml:"debugPort" json:"debugPort"`
	HeapSize       string                               `yaml:"heapSize" json:"heapSize"`
	MemoryLimit    string                               `yaml:"memoryLimit" json:"memoryLimit"`
	ServerPort     int                                  `yaml:"serverPort" json:"serverPort"`
	JavaOpts       string                               `yaml:"javaOpts" json:"javaOpts"`
	SpringProfiles string                               `yaml:"springProfiles" json:"springProfiles"`
}

type NamespaceConfig struct {
	APIVersion     string              `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`
	ID             string              `yaml:"id" json:"id"`
	Name           string              `yaml:"name" json:"name"`
	Snapshot       string              `yaml:"snapshot" json:"snapshot"`
	Template       string              `yaml:"template" json:"template"`
	Authentication AuthenticationProps `yaml:"authentication" json:"authentication"`
	BundleRef      bundle.BundleRef    `yaml:"bundleRef" json:"bundleRef"`
	PgAdmin        PgAdminProps        `yaml:"pgAdmin" json:"pgAdmin"`
	MongoDB        MongoDbProps        `yaml:"mongodb" json:"mongodb"`
	Proxy          ProxyProps          `yaml:"proxy" json:"proxy"`
	Webapps        map[string]WebappProps `yaml:"webapps,omitempty" json:"webapps,omitempty"`
}

func DefaultNamespaceConfig() NamespaceConfig {
	return NamespaceConfig{
		Authentication: AuthenticationProps{
			Type:  AuthBasic,
			Users: []string{"admin"},
		},
		PgAdmin: PgAdminProps{Enabled: true},
		Proxy:   ProxyProps{Port: 80},
	}
}

func LoadNamespaceConfig(path string) (*NamespaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read namespace config: %w", err)
	}
	return ParseNamespaceConfig(data)
}

func MarshalNamespaceConfig(cfg *NamespaceConfig) ([]byte, error) {
	out := *cfg
	if out.APIVersion == "" {
		out.APIVersion = "v1"
	}
	return yaml.Marshal(&out)
}

func ParseNamespaceConfig(data []byte) (*NamespaceConfig, error) {
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

// ValidateNamespaceConfig checks namespace config for common errors.
func ValidateNamespaceConfig(cfg *NamespaceConfig) error {
	if cfg.Proxy.Port < 1 || cfg.Proxy.Port > 65535 {
		return fmt.Errorf("proxy port must be 1-65535, got %d", cfg.Proxy.Port)
	}
	if cfg.Proxy.TLS.Enabled && cfg.Proxy.Host == "" {
		return fmt.Errorf("proxy host required when TLS is enabled")
	}
	if cfg.Proxy.TLS.LetsEncrypt {
		host := cfg.Proxy.Host
		if host == "" || host == "localhost" || host == "127.0.0.1" {
			return fmt.Errorf("Let's Encrypt requires a public hostname, got %q", host)
		}
	}
	if cfg.Authentication.Type == AuthBasic && len(cfg.Authentication.Users) == 0 {
		return fmt.Errorf("at least one user required for BASIC authentication")
	}
	return nil
}
