package setup

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type languageSetting struct{}

func (s *languageSetting) ID() string             { return "language" }
func (s *languageSetting) Title() string          { return i18n.T("setup.language.title") }
func (s *languageSetting) Description() string    { return i18n.T("setup.language.desc") }
func (s *languageSetting) TargetFile() TargetFile { return DaemonFile }

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
	options := make([]prompt.Option[string], 0, len(i18n.SupportedLocales)+1)
	for _, l := range i18n.SupportedLocales {
		options = append(options, prompt.Option[string]{Label: l.Name + " (" + l.Code + ")", Value: l.Code})
	}
	options = append(options, prompt.Option[string]{Label: i18n.T("setup.back"), Value: backValue})

	selected, err := (&prompt.Select[string]{
		Title:   i18n.T("setup.language.prompt"),
		Options: options,
		Height:  prompt.DefaultSelectHeight,
		Hints:   hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("language selection: %w", err)
	}
	if selected == backValue {
		return prompt.ErrCanceled
	}
	dcfg.Locale = selected
	return nil
}
