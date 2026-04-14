package setup

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
)

// adminPasswordSetting resets the ecos-app realm admin password via the
// daemon's keycloak admin endpoint. It is an actionSetting: Run() does the
// whole thing itself (prompt, call daemon API, report result) and skips the
// file-diff/confirm/reload flow used for file-backed settings.
type adminPasswordSetting struct{}

func (s *adminPasswordSetting) ID() string             { return "admin-password" }
func (s *adminPasswordSetting) Title() string          { return i18n.T("setup.admin_password.title") }
func (s *adminPasswordSetting) Description() string    { return i18n.T("setup.admin_password.desc") }
func (s *adminPasswordSetting) TargetFile() TargetFile { return NamespaceFile }

// isActionSetting marks this as an actionSetting — handled specially in
// runSingleSetting (no diff, no confirm, no file writes, no reload).
func (s *adminPasswordSetting) isActionSetting() {}

func (s *adminPasswordSetting) Available(_ *namespace.Config, _ []string) bool { return true }

// CurrentValue is intentionally generic — we don't want to leak the
// installed password anywhere, even in a masked form, from the menu.
func (s *adminPasswordSetting) CurrentValue(_ *namespace.Config, _ *config.DaemonConfig) string {
	return i18n.T("setup.admin_password.current")
}

func (s *adminPasswordSetting) Run(_ *setupContext, _ *namespace.Config, _ *config.DaemonConfig) error {
	// The reset requires a running daemon — kcadm.sh runs inside the
	// keycloak container, which only exists after the daemon has started
	// the namespace.
	c := client.TryNew(client.Options{})
	if c == nil {
		return errors.New(i18n.T("setup.admin_password.daemonNotRunning"))
	}
	defer c.Close()
	if !c.IsRunning() {
		return errors.New(i18n.T("setup.admin_password.daemonNotRunning"))
	}

	newPass, err := promptAdminPassword()
	if err != nil {
		return fmt.Errorf("admin password form: %w", err)
	}

	// Warn that this changes ALL admin panels at once, then ask for explicit
	// confirmation. No container restart: Keycloak/RabbitMQ/PgAdmin are
	// updated at runtime and webapps connect via the stable citeck SA.
	fmt.Println()                                       //nolint:forbidigo // CLI output
	fmt.Println(i18n.T("setup.admin_password.warning")) //nolint:forbidigo // CLI output
	fmt.Println()                                       //nolint:forbidigo // CLI output
	proceed, confirmErr := (&prompt.Confirm{
		Title:       i18n.T("setup.admin_password.confirmApply"),
		Affirmative: output.ConfirmYes,
		Negative:    output.ConfirmNo,
		Default:     true,
		Hints:       hints(),
	}).Run()
	if confirmErr != nil || !proceed {
		fmt.Println(i18n.T("setup.admin_password.canceled")) //nolint:forbidigo // CLI output
		return nil
	}

	fmt.Println(i18n.T("setup.admin_password.applying")) //nolint:forbidigo // CLI output
	if _, apiErr := c.SetAdminPassword(newPass); apiErr != nil {
		return fmt.Errorf("set admin password: %w", apiErr)
	}
	fmt.Println(i18n.T("setup.admin_password.applied")) //nolint:forbidigo // CLI output
	return nil
}

// promptAdminPassword asks the user to enter a new password manually or
// generate a random one. Returns the chosen password.
func promptAdminPassword() (string, error) {
	const (
		modeManual   = "manual"
		modeGenerate = "generate"
	)
	mode, err := (&prompt.Select[string]{
		Title: i18n.T("setup.admin_password.mode"),
		Options: []prompt.Option[string]{
			{Label: i18n.T("setup.admin_password.modeManual"), Value: modeManual},
			{Label: i18n.T("setup.admin_password.modeGenerate"), Value: modeGenerate},
		},
		Hints: hints(),
	}).Run()
	if err != nil {
		return "", fmt.Errorf("password mode selection: %w", err)
	}

	if mode == modeGenerate {
		pass, genErr := namespace.GenerateSimpleAdminPassword()
		if genErr != nil {
			return "", fmt.Errorf("generate password: %w", genErr)
		}
		fmt.Printf("\n  %s: %s\n\n", i18n.T("setup.admin_password.generated"), pass) //nolint:forbidigo // CLI output
		return pass, nil
	}

	newPass, err := (&prompt.Input{
		Title:       i18n.T("setup.admin_password.prompt"),
		Description: i18n.T("setup.admin_password.promptHint"),
		Password:    true,
		Validate: func(v string) error {
			if len(v) < 6 {
				return errors.New(i18n.T("setup.admin_password.tooShort"))
			}
			return nil
		},
		Hints: hints(),
	}).Run()
	if err != nil {
		return "", fmt.Errorf("password input: %w", err)
	}
	if _, err := (&prompt.Input{
		Title:    i18n.T("setup.admin_password.confirm"),
		Password: true,
		Validate: func(v string) error {
			if v != newPass {
				return errors.New(i18n.T("setup.admin_password.mismatch"))
			}
			return nil
		},
		Hints: hints(),
	}).Run(); err != nil {
		return "", fmt.Errorf("password input: %w", err)
	}
	return newPass, nil
}
