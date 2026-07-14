package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// DaemonConfig represents daemon.yml configuration.
type DaemonConfig struct {
	Locale     string           `yaml:"locale,omitempty" json:"locale,omitempty"` // UI language (en, ru, zh, es, de, fr, pt, ja)
	Server     ServerConfig     `yaml:"server" json:"server"`
	APIAuth    APIAuthConfig    `yaml:"api_auth,omitempty" json:"apiAuth,omitzero"`
	Reconciler ReconcilerConfig `yaml:"reconciler,omitempty" json:"reconciler,omitzero"`
	Docker     DockerConfig     `yaml:"docker,omitempty" json:"docker,omitzero"`
}

// ServerConfig holds server-level settings.
type ServerConfig struct {
	WebUI WebUIConfig `yaml:"webui" json:"webui"`
}

// WebUIConfig controls the embedded web UI. Note: Enabled is inert in server
// mode — the Web UI is not offered there and the runtime gate ignores this flag
// (see DefaultDaemonConfig and bootstrap startWebUI).
type WebUIConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

// APIAuthConfig controls the opt-in bearer-token authentication on the TCP
// Web UI/API transport (server mode). Default OFF — existing localhost flows
// keep working unchanged. When Enabled and Token is empty, the daemon
// generates a random 32-byte token on startup and persists it to
// conf/api-token (0600); see EnsureAPIToken. The Unix socket, the desktop
// wrapper path, and mTLS-authenticated clients are never token-gated.
type APIAuthConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Token   string `yaml:"token,omitempty" json:"token,omitempty"`
}

// ReconcilerConfig holds reconciler tuning.
type ReconcilerConfig struct {
	IntervalSeconds  int   `yaml:"interval,omitempty" json:"interval,omitempty"`               // default 60
	LivenessPeriodMs int   `yaml:"livenessPeriod,omitempty" json:"livenessPeriod,omitempty"`   // default 30000
	LivenessEnabled  *bool `yaml:"livenessEnabled,omitempty" json:"livenessEnabled,omitempty"` // default true
}

// DockerConfig holds Docker-level tuning.
type DockerConfig struct {
	PullConcurrency int `yaml:"pullConcurrency,omitempty" json:"pullConcurrency,omitempty"` // default 4
	StopTimeout     int `yaml:"stopTimeout,omitempty" json:"stopTimeout,omitempty"`         // 0 = Docker's own 10s default applies
}

// DefaultDaemonConfig returns the default daemon configuration.
//
// The Web UI is NOT offered in server mode: the CLI/TUI is the supported server
// interface, and the TCP listener would serve the full privileged API. The
// `server.webui.enabled` field is retained (and still parsed) but is inert in
// server mode — the runtime gate (bootstrap startWebUI) ignores it and only
// binds the listener via the explicit CITECK_SERVER_WEBUI dev/E2E hatch. Desktop
// mode is unaffected: the webview talks to the daemon over the Unix socket and
// the TCP listener stays off there too (except the CITECK_DESKTOP_TCP hatch).
func DefaultDaemonConfig() DaemonConfig {
	return DaemonConfig{
		Server: ServerConfig{
			WebUI: WebUIConfig{
				Enabled: false,
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

// LoadAPIToken returns the effective API auth token without generating one:
// an explicit api_auth.token in daemon.yml wins, otherwise the persisted
// token file (conf/api-token) is read. Used by the CLI (`citeck ui`), which
// must never mint a token of its own — the daemon owns generation.
func LoadAPIToken(cfg DaemonConfig) (string, error) {
	if cfg.APIAuth.Token != "" {
		return cfg.APIAuth.Token, nil
	}
	data, err := os.ReadFile(APITokenPath()) //nolint:gosec // G304: path is derived from internal config, not user input
	if err != nil {
		return "", fmt.Errorf("read api token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("api token file %s is empty", APITokenPath())
	}
	return token, nil
}

// EnsureAPIToken returns the effective API auth token, generating a random
// 32-byte one and persisting it to conf/api-token with 0600 permissions when
// neither daemon.yml api_auth.token nor the file provides one. `generated`
// reports whether a new token was minted on this call (the daemon logs the
// file path in that case). A read error other than not-exist is fatal rather
// than silently overwritten — the file may hold a token browsers already use.
func EnsureAPIToken(cfg DaemonConfig) (token string, generated bool, err error) {
	if cfg.APIAuth.Token != "" {
		return cfg.APIAuth.Token, false, nil
	}
	data, readErr := os.ReadFile(APITokenPath()) //nolint:gosec // G304: path is derived from internal config, not user input
	switch {
	case readErr == nil:
		if existing := strings.TrimSpace(string(data)); existing != "" {
			return existing, false, nil
		}
		// Empty file → fall through and regenerate.
	case !os.IsNotExist(readErr):
		return "", false, fmt.Errorf("read api token file: %w", readErr)
	}
	var b [32]byte
	if _, randErr := rand.Read(b[:]); randErr != nil {
		return "", false, fmt.Errorf("generate api token: %w", randErr)
	}
	token = hex.EncodeToString(b[:])
	if mkErr := os.MkdirAll(ConfDir(), 0o755); mkErr != nil { //nolint:gosec // conf dir needs 0o755 for readability
		return "", false, fmt.Errorf("create conf dir: %w", mkErr)
	}
	if writeErr := fsutil.AtomicWriteFile(APITokenPath(), []byte(token+"\n"), 0o600); writeErr != nil {
		return "", false, fmt.Errorf("persist api token: %w", writeErr)
	}
	return token, true, nil
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
