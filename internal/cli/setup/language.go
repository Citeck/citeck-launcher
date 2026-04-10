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
	options := make([]huh.Option[string], 0, len(i18n.SupportedLocales))
	for _, l := range i18n.SupportedLocales {
		options = append(options, huh.NewOption(l.Name+" ("+l.Code+")", l.Code))
	}

	err := huh.NewSelect[string]().
		Title(i18n.T("setup.language.prompt")).
		Options(options...).
		Value(&selected).
		WithTheme(output.HuhTheme).
		Run()
	if err != nil {
		return fmt.Errorf("language selection: %w", err)
	}
	dcfg.Locale = selected
	return nil
}
