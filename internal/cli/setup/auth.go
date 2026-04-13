package setup

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
)

type authSetting struct{}

func (s *authSetting) ID() string             { return "auth" }
func (s *authSetting) Title() string           { return i18n.T("setup.auth.title") }
func (s *authSetting) Description() string     { return i18n.T("setup.auth.desc") }
func (s *authSetting) TargetFile() TargetFile  { return NamespaceFile }

func (s *authSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *authSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	authType := string(cfg.Authentication.Type)
	if authType == "" {
		authType = "BASIC"
	}
	if cfg.Authentication.Type == namespace.AuthBasic && len(cfg.Authentication.Users) > 0 {
		return fmt.Sprintf("%s (%d users)", authType, len(cfg.Authentication.Users))
	}
	return authType
}

func (s *authSetting) Run(_ *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	var choice string
	err := output.RunField(huh.NewSelect[string]().
		Title(i18n.T("setup.auth.prompt")).
		Description(i18n.T("hint.select.setting")).
		Options(
			huh.NewOption("Keycloak SSO", string(namespace.AuthKeycloak)),
			huh.NewOption("Basic (username/password)", string(namespace.AuthBasic)),
			huh.NewOption(i18n.T("setup.back"), backValue),
		).
		Value(&choice))
	if err != nil {
		return fmt.Errorf("auth type selection: %w", err)
	}
	if choice == backValue {
		return huh.ErrUserAborted
	}

	cfg.Authentication.Type = namespace.AuthenticationType(choice)

	if cfg.Authentication.Type == namespace.AuthBasic {
		var usersStr string
		err = output.RunField(huh.NewInput().
			Title(i18n.T("setup.auth.users_prompt")).
			Description(i18n.T("hint.input")).
			Placeholder("admin, user1").
			Value(&usersStr).
			Validate(func(val string) error {
				if strings.TrimSpace(val) == "" {
					return fmt.Errorf("at least one user is required")
				}
				return nil
			}))
		if err != nil {
			return fmt.Errorf("users input: %w", err)
		}
		parts := strings.Split(usersStr, ",")
		users := make([]string, 0, len(parts))
		for _, p := range parts {
			if u := strings.TrimSpace(p); u != "" {
				users = append(users, u)
			}
		}
		cfg.Authentication.Users = users
	} else {
		cfg.Authentication.Users = nil
	}

	return nil
}
