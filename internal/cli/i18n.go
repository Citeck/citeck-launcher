package cli

import "github.com/citeck/citeck-launcher/internal/i18n"

// SupportedLocales is the single source of truth for all supported languages.
var SupportedLocales = i18n.SupportedLocales

// LocaleInfo describes a supported locale.
type LocaleInfo = i18n.LocaleInfo

func initI18n(locale string)              { i18n.InitI18n(locale) }
func ensureI18n()                         { i18n.EnsureI18n() }
func t(key string, args ...string) string { return i18n.T(key, args...) }
