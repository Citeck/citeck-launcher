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

type emailSetting struct{}

func (s *emailSetting) ID() string             { return "email" }
func (s *emailSetting) Title() string           { return i18n.T("setup.email.title") }
func (s *emailSetting) Description() string     { return i18n.T("setup.email.desc") }
func (s *emailSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *emailSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *emailSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.Email == nil {
		return i18n.T("setup.value.not_configured")
	}
	return fmt.Sprintf("%s:%d", cfg.Email.Host, cfg.Email.Port)
}

// emailPreset holds SMTP defaults for a well-known mail provider.
type emailPreset struct {
	Name string
	Host string
	Port int
	TLS  bool
}

var emailPresets = []emailPreset{
	{Name: "Gmail", Host: "smtp.gmail.com", Port: 587, TLS: true},
	{Name: "Yandex", Host: "smtp.yandex.ru", Port: 465, TLS: true},
	{Name: "Mail.ru", Host: "smtp.mail.ru", Port: 465, TLS: true},
	{Name: "Rambler", Host: "smtp.rambler.ru", Port: 465, TLS: true},
	{Name: "Outlook", Host: "smtp.office365.com", Port: 587, TLS: true},
}

func (s *emailSetting) Run(sctx *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	// If already configured, offer edit/remove.
	if cfg.Email != nil {
		var action string
		err := huh.NewSelect[string]().
			Title(i18n.T("setup.email.action")).
			Options(
				huh.NewOption(i18n.T("setup.email.edit"), "edit"),
				huh.NewOption(i18n.T("setup.email.remove"), "remove"),
				huh.NewOption(i18n.T("setup.back"), backValue),
			).
			Value(&action).
			WithTheme(output.HuhTheme).
		Run()
		if err != nil {
			return fmt.Errorf("email action selection: %w", err)
		}
		if action == backValue {
			return huh.ErrUserAborted
		}
		if action == "remove" {
			cfg.Email = nil
			return nil
		}
	}

	email := &namespace.EmailConfig{Port: 587, TLS: true}
	if cfg.Email != nil {
		email = cfg.Email
	}

	// Preset selection (only for new config or when host is empty).
	if cfg.Email == nil || cfg.Email.Host == "" {
		email = selectEmailPreset(email)
	}

	var host, portStr, username, password, from string
	var useTLS bool

	host = email.Host
	portStr = strconv.Itoa(email.Port)
	username = email.Username
	from = email.From
	useTLS = email.TLS

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title(i18n.T("setup.email.host")).Value(&host).Validate(notEmpty),
			huh.NewInput().Title(i18n.T("setup.email.port")).Value(&portStr).Validate(validatePort),
			huh.NewInput().Title(i18n.T("setup.email.from")).Value(&from).Validate(notEmpty),
			huh.NewInput().Title(i18n.T("setup.email.username")).Value(&username).Description(i18n.T("setup.email.username_hint")),
			huh.NewInput().Title(i18n.T("setup.email.password")).Value(&password).EchoMode(huh.EchoModePassword),
			huh.NewConfirm().Title(i18n.T("setup.email.tls")).Value(&useTLS),
		),
	).WithTheme(output.HuhTheme).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}

	port, _ := strconv.Atoi(strings.TrimSpace(portStr)) // error safe: validatePort already checked format
	cfg.Email = &namespace.EmailConfig{
		Host:     strings.TrimSpace(host),
		Port:     port,
		Username: strings.TrimSpace(username),
		From:     strings.TrimSpace(from),
		TLS:      useTLS,
	}

	// Secret handling: store password via sctx.PendingSecrets for later write by setup.go.
	if password != "" {
		cfg.Email.Password = "secret:email.password"
		sctx.PendingSecrets["email.password"] = password
	} else if email.Password != "" {
		// Keep existing secret reference.
		cfg.Email.Password = email.Password
	}

	return nil
}

// selectEmailPreset shows a preset picker and returns a pre-filled EmailConfig.
// Returns the original email config if the user selects "Custom" or cancels.
func selectEmailPreset(email *namespace.EmailConfig) *namespace.EmailConfig {
	const customValue = "_custom"

	options := make([]huh.Option[string], 0, len(emailPresets)+1)
	for _, p := range emailPresets {
		label := fmt.Sprintf("%s (%s:%d)", p.Name, p.Host, p.Port)
		options = append(options, huh.NewOption(label, p.Host))
	}
	options = append(options, huh.NewOption(i18n.T("setup.email.custom"), customValue))

	var selected string
	err := huh.NewSelect[string]().
		Title(i18n.T("setup.email.preset")).
		Options(options...).
		Value(&selected).
		WithTheme(output.HuhTheme).
		Run()
	if err != nil || selected == customValue {
		return email
	}

	for _, p := range emailPresets {
		if p.Host == selected {
			return &namespace.EmailConfig{
				Host: p.Host,
				Port: p.Port,
				TLS:  p.TLS,
			}
		}
	}
	return email
}

// notEmpty validates that a field is not empty after trimming whitespace.
// Shared by email, tls, and s3 settings.
func notEmpty(val string) error {
	if strings.TrimSpace(val) == "" {
		return fmt.Errorf("this field is required")
	}
	return nil
}

// validatePort validates that a string is a valid port number (1-65535).
// Shared by email and port settings.
func validatePort(val string) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return fmt.Errorf("port is required")
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fmt.Errorf("invalid number")
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}
