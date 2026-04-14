package setup

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type tlsSetting struct{}

func (s *tlsSetting) ID() string             { return "tls" }
func (s *tlsSetting) Title() string          { return i18n.T("setup.tls.title") }
func (s *tlsSetting) Description() string    { return i18n.T("setup.tls.desc") }
func (s *tlsSetting) TargetFile() TargetFile { return NamespaceFile }

func (s *tlsSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *tlsSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	tls := cfg.Proxy.TLS
	if !tls.Enabled {
		return i18n.T("setup.value.disabled")
	}
	if tls.LetsEncrypt {
		return "Let's Encrypt"
	}
	if tls.CertPath != "" {
		return i18n.T("setup.value.custom_cert")
	}
	return i18n.T("setup.value.self_signed")
}

func (s *tlsSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	const (
		optLE         = "letsencrypt"
		optSelfSigned = "selfsigned"
		optCustom     = "custom"
		optHTTP       = "http"
	)

	tlsOptions := []prompt.Option[string]{
		{Label: "Let's Encrypt (automatic HTTPS)", Value: optLE},
		{Label: i18n.T("setup.tls.selfsigned"), Value: optSelfSigned},
		{Label: i18n.T("setup.tls.custom"), Value: optCustom},
		{Label: "HTTP only (no TLS)", Value: optHTTP},
		{Label: i18n.T("setup.back"), Value: backValue},
	}
	choice, err := (&prompt.Select[string]{
		Title:   i18n.T("setup.tls.prompt"),
		Options: tlsOptions,
		Height:  prompt.DefaultSelectHeight,
		Hints:   hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("tls selection: %w", err)
	}
	if choice == backValue {
		return prompt.ErrCanceled
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
		certPath, cerr := (&prompt.Input{
			Title:    i18n.T("setup.tls.cert_path"),
			Validate: notEmpty,
			Hints:    hints(),
		}).Run()
		if cerr != nil {
			return fmt.Errorf("tls cert form: %w", cerr)
		}
		keyPath, kerr := (&prompt.Input{
			Title:    i18n.T("setup.tls.key_path"),
			Validate: notEmpty,
			Hints:    hints(),
		}).Run()
		if kerr != nil {
			return fmt.Errorf("tls cert form: %w", kerr)
		}
		cfg.Proxy.TLS.Enabled = true
		cfg.Proxy.TLS.CertPath = certPath
		cfg.Proxy.TLS.KeyPath = keyPath
	case optHTTP:
		// TLS disabled (default zero value).
	}

	return nil
}
