package setup

import (
	"fmt"
	"net"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
)

type hostnameSetting struct{}

func (s *hostnameSetting) ID() string             { return "hostname" }
func (s *hostnameSetting) Title() string           { return i18n.T("setup.hostname.title") }
func (s *hostnameSetting) Description() string     { return i18n.T("setup.hostname.desc") }
func (s *hostnameSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *hostnameSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *hostnameSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.Proxy.Host == "" {
		return i18n.T("setup.value.not_configured")
	}
	return cfg.Proxy.Host
}

func (s *hostnameSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	var host string
	err := huh.NewInput().
		Title(i18n.T("setup.hostname.prompt")).
		Value(&host).
		Placeholder(cfg.Proxy.Host).
		Validate(func(val string) error {
			val = strings.TrimSpace(val)
			if val == "" {
				return fmt.Errorf("hostname is required")
			}
			// Allow IP addresses.
			if ip := net.ParseIP(val); ip != nil {
				return nil
			}
			// Validate hostname length.
			if len(val) > 253 {
				return fmt.Errorf("hostname too long")
			}
			return nil
		}).
		WithTheme(output.HuhTheme).
		Run()
	if err != nil {
		return fmt.Errorf("hostname input: %w", err)
	}
	cfg.Proxy.Host = strings.TrimSpace(host)
	return nil
}
