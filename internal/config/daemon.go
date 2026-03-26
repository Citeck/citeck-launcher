package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DaemonConfig represents daemon.yml configuration.
type DaemonConfig struct {
	Server ServerConfig `yaml:"server" json:"server"`
}

// ServerConfig holds server-level settings.
type ServerConfig struct {
	WebUI WebUIConfig `yaml:"webui" json:"webui"`
	Token string      `yaml:"token,omitempty" json:"token,omitempty"` // auth token for TCP connections
}

// WebUIConfig controls the embedded web UI.
type WebUIConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

// DefaultDaemonConfig returns the default daemon configuration.
func DefaultDaemonConfig() DaemonConfig {
	return DaemonConfig{
		Server: ServerConfig{
			WebUI: WebUIConfig{
				Enabled: true,
				Listen:  "127.0.0.1:8088",
			},
		},
	}
}

// LoadDaemonConfig reads daemon.yml from the conf dir. Returns defaults if file doesn't exist.
func LoadDaemonConfig() (DaemonConfig, error) {
	cfg := DefaultDaemonConfig()
	path := DaemonConfigPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read daemon config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse daemon config: %w", err)
	}

	// Ensure defaults for empty values
	if cfg.Server.WebUI.Listen == "" {
		cfg.Server.WebUI.Listen = "127.0.0.1:8088"
	}

	return cfg, nil
}

// SaveDaemonConfig writes daemon.yml to the conf dir.
func SaveDaemonConfig(cfg DaemonConfig) error {
	path := DaemonConfigPath()
	if err := os.MkdirAll(ConfDir(), 0o755); err != nil {
		return fmt.Errorf("create conf dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal daemon config: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}
