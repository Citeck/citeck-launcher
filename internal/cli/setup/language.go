package setup

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
)

type languageSetting struct{}

func (s *languageSetting) ID() string             { return "language" }
func (s *languageSetting) Title() string           { return i18n.T("setup.language.title") }
func (s *languageSetting) Description() string     { return i18n.T("setup.language.desc") }
func (s *languageSetting) TargetFile() TargetFile  { return DaemonFile }

func (s *languageSetting) Available(_ *namespace.Config, _ []string) bool { return true }

func (s *languageSetting) CurrentValue(_ *namespace.Config, dcfg *config.DaemonConfig) string {
	locale := dcfg.Locale
	if locale == "" {
		locale = "en"
	}
	for _, l := range i18n.SupportedLocales {
		if l.Code == locale {
			return l.Name + " (" + l.Code + ")"
		}
	}
	return locale
}

func (s *languageSetting) Run(_ *setupContext, _ *namespace.Config, dcfg *config.DaemonConfig) error {
	var selected string
	options := make([]huh.Option[string], 0, len(i18n.SupportedLocales)+1)
	for _, l := range i18n.SupportedLocales {
		options = append(options, huh.NewOption(l.Name+" ("+l.Code+")", l.Code))
	}
	options = append(options, huh.NewOption(i18n.T("setup.back"), backValue))

	sel := huh.NewSelect[string]().
		Title(i18n.T("setup.language.prompt")).
		Description(i18n.T("hint.select.setting")).
		Options(options...).
		Value(&selected)
	sel = output.ApplySelectHeight(sel, len(options))
	err := output.RunField(sel)
	if err != nil {
		return fmt.Errorf("language selection: %w", err)
	}
	if selected == backValue {
		return huh.ErrUserAborted
	}
	dcfg.Locale = selected
	return nil
}
