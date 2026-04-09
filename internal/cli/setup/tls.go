package setup

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
)

type tlsSetting struct{}

func (s *tlsSetting) ID() string             { return "tls" }
func (s *tlsSetting) Title() string           { return i18n.T("setup.tls.title") }
func (s *tlsSetting) Description() string     { return i18n.T("setup.tls.desc") }
func (s *tlsSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *tlsSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *tlsSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	tls := cfg.Proxy.TLS
	if !tls.Enabled {
		return "disabled"
	}
	if tls.LetsEncrypt {
		return "Let's Encrypt"
	}
	if tls.CertPath != "" {
		return "custom certificate"
	}
	return "self-signed"
}

func (s *tlsSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	const (
		optLE         = "letsencrypt"
		optSelfSigned = "selfsigned"
		optCustom     = "custom"
		optHTTP       = "http"
	)

	var choice string
	err := huh.NewSelect[string]().
		Title(i18n.T("setup.tls.prompt")).
		Options(
			huh.NewOption("Let's Encrypt (automatic HTTPS)", optLE),
			huh.NewOption(i18n.T("setup.tls.selfsigned"), optSelfSigned),
			huh.NewOption(i18n.T("setup.tls.custom"), optCustom),
			huh.NewOption("HTTP only (no TLS)", optHTTP),
		).
		Value(&choice).
		Run()
	if err != nil {
		return fmt.Errorf("tls selection: %w", err)
	}

	// Reset TLS config to zero value before applying selection.
	cfg.Proxy.TLS = namespace.TlsConfig{}

	switch choice {
	case optLE:
		cfg.Proxy.TLS.Enabled = true
		cfg.Proxy.TLS.LetsEncrypt = true
	case optSelfSigned:
		cfg.Proxy.TLS.Enabled = true
	case optCustom:
		var certPath, keyPath string
		err = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title(i18n.T("setup.tls.cert_path")).Value(&certPath).Validate(notEmpty),
				huh.NewInput().Title(i18n.T("setup.tls.key_path")).Value(&keyPath).Validate(notEmpty),
			),
		).Run()
		if err != nil {
			return fmt.Errorf("tls cert form: %w", err)
		}
		cfg.Proxy.TLS.Enabled = true
		cfg.Proxy.TLS.CertPath = certPath
		cfg.Proxy.TLS.KeyPath = keyPath
	case optHTTP:
		// TLS disabled (default zero value).
	}

	return nil
}
