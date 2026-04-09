package setup

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
)

type portSetting struct{}

func (s *portSetting) ID() string             { return "port" }
func (s *portSetting) Title() string           { return i18n.T("setup.port.title") }
func (s *portSetting) Description() string     { return i18n.T("setup.port.desc") }
func (s *portSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *portSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *portSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.Proxy.Port == 0 {
		return i18n.T("setup.value.not_configured")
	}
	return strconv.Itoa(cfg.Proxy.Port)
}

func (s *portSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	var portStr string
	err := huh.NewInput().
		Title(i18n.T("setup.port.prompt")).
		Value(&portStr).
		Placeholder(strconv.Itoa(cfg.Proxy.Port)).
		Validate(validatePort).
		Run()
	if err != nil {
		return fmt.Errorf("port input: %w", err)
	}
	port, _ := strconv.Atoi(strings.TrimSpace(portStr)) // error safe: validatePort already checked format
	cfg.Proxy.Port = port
	return nil
}
