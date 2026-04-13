package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// DaemonConfig represents daemon.yml configuration.
type DaemonConfig struct {
	Locale     string           `yaml:"locale,omitempty" json:"locale,omitempty"` // UI language (en, ru, zh, es, de, fr, pt, ja)
	Server     ServerConfig     `yaml:"server" json:"server"`
	Reconciler ReconcilerConfig `yaml:"reconciler,omitempty" json:"reconciler,omitzero"`
	Docker     DockerConfig     `yaml:"docker,omitempty" json:"docker,omitzero"`
}

// ServerConfig holds server-level settings.
type ServerConfig struct {
	WebUI WebUIConfig `yaml:"webui" json:"webui"`
}

// WebUIConfig controls the embedded web UI.
type WebUIConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

// ReconcilerConfig holds reconciler tuning.
type ReconcilerConfig struct {
	IntervalSeconds  int   `yaml:"interval,omitempty" json:"interval,omitempty"`           // default 60
	LivenessPeriodMs int   `yaml:"livenessPeriod,omitempty" json:"livenessPeriod,omitempty"` // default 30000
	LivenessEnabled  *bool `yaml:"livenessEnabled,omitempty" json:"livenessEnabled,omitempty"` // default true
}

// DockerConfig holds Docker-level tuning.
type DockerConfig struct {
	PullConcurrency int `yaml:"pullConcurrency,omitempty" json:"pullConcurrency,omitempty"` // default 4
	StopTimeout     int `yaml:"stopTimeout,omitempty" json:"stopTimeout,omitempty"`         // default 15
}

// DefaultDaemonConfig returns the default daemon configuration.
func DefaultDaemonConfig() DaemonConfig {
	return DaemonConfig{
		Server: ServerConfig{
			WebUI: WebUIConfig{
				Enabled: true,
				Listen:  "127.0.0.1:7088",
			},
		},
	}
}

// LoadDaemonConfig reads daemon.yml from the conf dir. Returns defaults if file doesn't exist.
func LoadDaemonConfig() (DaemonConfig, error) {
	cfg := DefaultDaemonConfig()
	path := DaemonConfigPath()

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is derived from internal config, not user input
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
		cfg.Server.WebUI.Listen = "127.0.0.1:7088"
	}

	// Validate listen address format
	if cfg.Server.WebUI.Enabled {
		host, _, err := net.SplitHostPort(cfg.Server.WebUI.Listen)
		if err != nil {
			return cfg, fmt.Errorf("invalid webui listen address %q: %w", cfg.Server.WebUI.Listen, err)
		}
		if host != "" && host != "0.0.0.0" && host != "::" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
			// Non-localhost binding — warn if no mTLS certs
			caDir := WebUICADir()
			if entries, err := os.ReadDir(caDir); err != nil || len(entries) == 0 {
				slog.Warn("Non-localhost listen address without mTLS client certs",
					"listen", cfg.Server.WebUI.Listen,
					"caDir", caDir,
					"hint", "run: citeck webui cert --name admin")
			}
		}
	}

	return cfg, nil
}

// SaveDaemonConfig writes daemon.yml to the conf dir.
func SaveDaemonConfig(cfg DaemonConfig) error {
	path := DaemonConfigPath()
	if err := os.MkdirAll(ConfDir(), 0o755); err != nil { //nolint:gosec // conf dir needs 0o755 for readability
		return fmt.Errorf("create conf dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal daemon config: %w", err)
	}

	if err := fsutil.AtomicWriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write daemon config: %w", err)
	}
	return nil
}
