package setup

import (
	"errors"
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type authSetting struct{}

func (s *authSetting) ID() string             { return "auth" }
func (s *authSetting) Title() string          { return i18n.T("setup.auth.title") }
func (s *authSetting) Description() string    { return i18n.T("setup.auth.desc") }
func (s *authSetting) TargetFile() TargetFile { return NamespaceFile }

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
	choice, err := (&prompt.Select[string]{
		Title: i18n.T("setup.auth.prompt"),
		Options: []prompt.Option[string]{
			{Label: "Keycloak SSO", Value: string(namespace.AuthKeycloak)},
			{Label: "Basic (username/password)", Value: string(namespace.AuthBasic)},
			{Label: i18n.T("setup.back"), Value: backValue},
		},
		Hints: hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("auth type selection: %w", err)
	}
	if choice == backValue {
		return prompt.ErrCanceled
	}

	cfg.Authentication.Type = namespace.AuthenticationType(choice)

	if cfg.Authentication.Type == namespace.AuthBasic {
		usersStr, inpErr := (&prompt.Input{
			Title:       i18n.T("setup.auth.users_prompt"),
			Placeholder: "admin, user1",
			Validate: func(val string) error {
				if strings.TrimSpace(val) == "" {
					return errors.New(i18n.T("setup.auth.usersRequired"))
				}
				return nil
			},
			Hints: hints(),
		}).Run()
		if inpErr != nil {
			return fmt.Errorf("users input: %w", inpErr)
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
