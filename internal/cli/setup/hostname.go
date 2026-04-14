package setup

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// hostnameUnsafeChars enumerates bytes that must never appear in a
// user-supplied hostname. They are shell metacharacters or whitespace
// which, if permitted, could break out of bash single-/double-quoted
// contexts in generated scripts (proxy config, keycloak init, etc.) or
// feed arbitrary commands to helpers invoked with the hostname. The
// `shquote` helper in internal/appfiles defends the keycloak script
// against the same class of inputs; this validation is an extra, earlier
// layer for all hostname consumers (proxy config, certs, URLs).
const hostnameUnsafeChars = "\"'\\$`;&| \t\r\n"

// validateHostname returns nil if val is a usable hostname or IP for
// proxy configuration. It enforces non-empty, bounded length, and the
// absence of shell-metacharacter / whitespace bytes defined by
// hostnameUnsafeChars. IP literals are accepted verbatim.
func validateHostname(val string) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return errors.New(i18n.T("validate.required"))
	}
	if idx := strings.IndexAny(val, hostnameUnsafeChars); idx >= 0 {
		return errors.New(i18n.T("setup.hostname.forbiddenChar",
			"char", fmt.Sprintf("%q", val[idx]),
			"pos", fmt.Sprintf("%d", idx)))
	}
	// Accept IPs verbatim once we've ruled out metacharacters.
	if ip := net.ParseIP(val); ip != nil {
		return nil
	}
	if len(val) > 253 {
		return errors.New(i18n.T("setup.hostname.tooLong"))
	}
	return nil
}

type hostnameSetting struct{}

func (s *hostnameSetting) ID() string             { return "hostname" }
func (s *hostnameSetting) Title() string          { return i18n.T("setup.hostname.title") }
func (s *hostnameSetting) Description() string    { return i18n.T("setup.hostname.desc") }
func (s *hostnameSetting) TargetFile() TargetFile { return NamespaceFile }

func (s *hostnameSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *hostnameSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.Proxy.Host == "" {
		return i18n.T("setup.value.not_configured")
	}
	return cfg.Proxy.Host
}

func (s *hostnameSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	host, err := (&prompt.Input{
		Title:    i18n.T("setup.hostname.prompt"),
		Value:    cfg.Proxy.Host,
		Validate: validateHostname,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("hostname input: %w", err)
	}
	cfg.Proxy.Host = strings.TrimSpace(host)
	return nil
}
