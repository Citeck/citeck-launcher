package cli

import (
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
)

// SupportedLocales is the single source of truth for all supported languages.
var SupportedLocales = i18n.SupportedLocales

// LocaleInfo describes a supported locale.
type LocaleInfo = i18n.LocaleInfo

func initI18n(locale string) {
	i18n.InitI18n(locale)
	syncConfirmLabels()
}

func ensureI18n() {
	i18n.EnsureI18n()
	syncConfirmLabels()
}

func t(key string, args ...string) string { return i18n.T(key, args...) }

func syncConfirmLabels() {
	if v := i18n.T("confirm.yes"); v != "confirm.yes" {
		output.ConfirmYes = v
	}
	if v := i18n.T("confirm.no"); v != "confirm.no" {
		output.ConfirmNo = v
	}
}
