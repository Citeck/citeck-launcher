package setup

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
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
	// Start with an empty input so typing replaces the shown default instead
	// of requiring backspace. The current value is rendered as a placeholder
	// and used as fallback when the field is left empty.
	//
	// When the current port is unset (0) — which should not happen in practice
	// because `citeck install` always configures a port — treat it as "no
	// default": render no placeholder and make the input required so empty
	// submissions are rejected instead of silently producing an invalid "0".
	var placeholder string
	if cfg.Proxy.Port != 0 {
		placeholder = strconv.Itoa(cfg.Proxy.Port)
	}
	var portStr string
	err := output.RunField(huh.NewInput().
		Title(i18n.T("setup.port.prompt")).
		Description(i18n.T("hint.input")).
		Value(&portStr).
		Placeholder(placeholder).
		Validate(validatePortWithDefault(placeholder)))
	if err != nil {
		return fmt.Errorf("port input: %w", err)
	}
	if strings.TrimSpace(portStr) == "" {
		// Only reachable when placeholder is non-empty (otherwise the
		// validator rejects empty input); fall back to current value.
		portStr = placeholder
	}
	port, _ := strconv.Atoi(strings.TrimSpace(portStr)) // error safe: validatePort already checked format
	cfg.Proxy.Port = port
	return nil
}
