package setup

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
)

type emailSetting struct{}

func (s *emailSetting) ID() string             { return "email" }
func (s *emailSetting) Title() string          { return i18n.T("setup.email.title") }
func (s *emailSetting) Description() string    { return i18n.T("setup.email.desc") }
func (s *emailSetting) TargetFile() TargetFile { return NamespaceFile }

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
		action, err := (&prompt.Select[string]{
			Title: i18n.T("setup.email.action"),
			Options: []prompt.Option[string]{
				{Label: i18n.T("setup.email.edit"), Value: "edit"},
				{Label: i18n.T("setup.email.remove"), Value: "remove"},
				{Label: i18n.T("setup.back"), Value: backValue},
			},
			Hints: hints(),
		}).Run()
		if err != nil {
			return fmt.Errorf("email action selection: %w", err)
		}
		if action == backValue {
			return prompt.ErrCanceled
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

	// Leave portStr empty so typing the value replaces the prefill instead
	// of requiring backspace; the preset port renders via Placeholder, and we
	// fall back to it when the user leaves the field empty.
	portPlaceholder := strconv.Itoa(email.Port)

	host, err := (&prompt.Input{
		Title:    i18n.T("setup.email.host"),
		Value:    email.Host,
		Validate: notEmpty,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	portStr, err := (&prompt.Input{
		Title:       i18n.T("setup.email.port"),
		Placeholder: portPlaceholder,
		Validate:    validatePortWithDefault(portPlaceholder),
		Hints:       hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	from, err := (&prompt.Input{
		Title:    i18n.T("setup.email.from"),
		Value:    email.From,
		Validate: notEmpty,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	username, err := (&prompt.Input{
		Title:       i18n.T("setup.email.username"),
		Description: i18n.T("setup.email.username_hint"),
		Value:       email.Username,
		Hints:       hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	password, err := (&prompt.Input{
		Title:    i18n.T("setup.email.password"),
		Password: true,
		Hints:    hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	useTLS, err := (&prompt.Confirm{
		Title:       i18n.T("setup.email.tls"),
		Affirmative: output.ConfirmYes,
		Negative:    output.ConfirmNo,
		Default:     email.TLS,
		Hints:       hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}

	// Startup-notification probe: asks the notifications service to send a
	// test email on every boot. Pre-fill the recipient from `from` so the
	// common "send to myself" case is a single Enter press.
	prevStartup := email.StartupNotification
	startupDefault := false
	startupRecipientDefault := from
	if prevStartup != nil {
		startupDefault = prevStartup.Enabled
		if prevStartup.Recipient != "" {
			startupRecipientDefault = prevStartup.Recipient
		}
	}
	startupEnabled, err := (&prompt.Confirm{
		Title:       i18n.T("setup.email.startup.prompt"),
		Affirmative: output.ConfirmYes,
		Negative:    output.ConfirmNo,
		Default:     startupDefault,
		Hints:       hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("email form: %w", err)
	}
	var startupRecipient string
	if startupEnabled {
		startupRecipient, err = (&prompt.Input{
			Title:    i18n.T("setup.email.startup.recipient"),
			Value:    startupRecipientDefault,
			Validate: notEmpty,
			Hints:    hints(),
		}).Run()
		if err != nil {
			return fmt.Errorf("email form: %w", err)
		}
	}

	// If the user left the port empty, apply the placeholder default.
	if strings.TrimSpace(portStr) == "" {
		portStr = portPlaceholder
	}
	port, _ := strconv.Atoi(strings.TrimSpace(portStr)) // error safe: validatePort already checked format
	applyEmailSetting(sctx, cfg, email, host, port, username, from, password, useTLS,
		startupEnabled, startupRecipient)
	return nil
}

// applyEmailSetting writes the parsed form values into cfg and sctx.PendingSecrets.
// Plain passwords are never written to cfg — only "secret:email.password" refs,
// which are resolved at container-start time by the generator (applyEmailConfig).
// Extracted from Run() so the behavior can be unit tested without driving the TUI.
func applyEmailSetting(sctx *setupContext, cfg *namespace.Config, prev *namespace.EmailConfig,
	host string, port int, username, from, password string, useTLS bool,
	startupEnabled bool, startupRecipient string,
) {
	cfg.Email = &namespace.EmailConfig{
		Host:     strings.TrimSpace(host),
		Port:     port,
		Username: strings.TrimSpace(username),
		From:     strings.TrimSpace(from),
		TLS:      useTLS,
	}
	if startupEnabled {
		cfg.Email.StartupNotification = &namespace.StartupNotificationConfig{
			Enabled:   true,
			Recipient: strings.TrimSpace(startupRecipient),
		}
	}

	// Secret handling: store password via sctx.PendingSecrets for later write by setup.go.
	if password != "" {
		cfg.Email.Password = "secret:email.password"
		sctx.PendingSecrets["email.password"] = password
	} else if prev != nil && prev.Password != "" {
		// Keep existing secret reference.
		cfg.Email.Password = prev.Password
	}
}

// selectEmailPreset shows a preset picker and returns a pre-filled EmailConfig.
// Returns the original email config if the user selects "Custom" or cancels.
func selectEmailPreset(email *namespace.EmailConfig) *namespace.EmailConfig {
	const customValue = "_custom"

	options := make([]prompt.Option[string], 0, len(emailPresets)+1)
	for _, p := range emailPresets {
		label := fmt.Sprintf("%s (%s:%d)", p.Name, p.Host, p.Port)
		options = append(options, prompt.Option[string]{Label: label, Value: p.Host})
	}
	options = append(options, prompt.Option[string]{Label: i18n.T("setup.email.custom"), Value: customValue})

	selected, err := (&prompt.Select[string]{
		Title:   i18n.T("setup.email.preset"),
		Options: options,
		Height:  prompt.DefaultSelectHeight,
		Hints:   hints(),
	}).Run()
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
		return errors.New(i18n.T("validate.required"))
	}
	return nil
}

// validatePort validates that a string is a valid port number (1-65535).
// Shared by email and port settings.
func validatePort(val string) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return errors.New(i18n.T("validate.required"))
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return errors.New(i18n.T("setup.port.invalidNumber"))
	}
	if n < 1 || n > 65535 {
		return errors.New(i18n.T("setup.port.outOfRange"))
	}
	return nil
}

// validatePortWithDefault is like validatePort but treats an empty value as
// valid, letting the caller fall back to a placeholder-rendered default.
func validatePortWithDefault(defaultPort string) func(string) error {
	return func(val string) error {
		if strings.TrimSpace(val) == "" {
			if defaultPort == "" {
				return errors.New(i18n.T("validate.required"))
			}
			return nil
		}
		return validatePort(val)
	}
}
