package cli

import (
	"embed"
	"encoding/json"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
)

//go:embed locales/*.json
var localeFS embed.FS

// LocaleInfo describes a supported locale.
type LocaleInfo struct {
	Code string // e.g. "en"
	Name string // native name, e.g. "English"
}

// SupportedLocales is the single source of truth for all supported languages.
// Used by the install wizard, tests, and web UI locale sync.
var SupportedLocales = []LocaleInfo{
	{Code: "en", Name: "English"},
	{Code: "ru", Name: "Русский"},
	{Code: "zh", Name: "简体中文"},
	{Code: "es", Name: "Español"},
	{Code: "de", Name: "Deutsch"},
	{Code: "fr", Name: "Français"},
	{Code: "pt", Name: "Português"},
	{Code: "ja", Name: "日本語"},
}

var (
	cliTranslations map[string]string
	cliFallback     map[string]string
)

// initI18n loads the CLI translations for the given locale.
// Falls back to English for missing keys.
func initI18n(locale string) {
	cliFallback = loadLocale("en")
	cliTranslations = loadLocale(locale)
}

func loadLocale(locale string) map[string]string {
	data, err := localeFS.ReadFile("locales/" + locale + ".json")
	if err != nil {
		return nil
	}
	var m map[string]string
	if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
		return nil
	}
	return m
}

// ensureI18n initializes i18n from daemon.yml locale if not already loaded.
// Safe to call multiple times from the main goroutine — not goroutine-safe.
// CLI commands run sequentially on a single goroutine (cobra RunE).
func ensureI18n() {
	if cliTranslations != nil {
		return
	}
	locale := "en"
	if cfg, err := config.LoadDaemonConfig(); err == nil && cfg.Locale != "" {
		locale = cfg.Locale
	}
	initI18n(locale)
}

// t returns the translated string for the given key.
// Supports {param} interpolation: t("key", "param", "value", "param2", "value2")
func t(key string, args ...string) string {
	text := cliTranslations[key]
	if text == "" {
		text = cliFallback[key]
	}
	if text == "" {
		return key
	}
	for i := 0; i+1 < len(args); i += 2 {
		text = strings.ReplaceAll(text, "{"+args[i]+"}", args[i+1])
	}
	return text
}
