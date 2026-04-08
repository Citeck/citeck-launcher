package cli

import (
	"encoding/json"
	"sort"
	"testing"
)

func TestLocaleCompleteness(t *testing.T) {
	// Load reference (en) keys
	enData, err := localeFS.ReadFile("locales/en.json")
	if err != nil {
		t.Fatalf("failed to read en.json: %v", err)
	}
	var enKeys map[string]string
	if err := json.Unmarshal(enData, &enKeys); err != nil {
		t.Fatalf("failed to parse en.json: %v", err)
	}

	refKeys := sortedKeys(enKeys)
	if len(refKeys) == 0 {
		t.Fatal("en.json has no keys")
	}

	for _, loc := range SupportedLocales {
		if loc.Code == "en" {
			continue
		}
		t.Run(loc.Code, func(t *testing.T) {
			data, readErr := localeFS.ReadFile("locales/" + loc.Code + ".json")
			if readErr != nil {
				t.Fatalf("failed to read %s.json: %v", loc.Code, readErr)
			}
			var localeKeys map[string]string
			if jsonErr := json.Unmarshal(data, &localeKeys); jsonErr != nil {
				t.Fatalf("failed to parse %s.json: %v", loc.Code, jsonErr)
			}

			// Check all en keys exist in this locale
			for _, key := range refKeys {
				if _, ok := localeKeys[key]; !ok {
					t.Errorf("missing key %q in %s.json", key, loc.Code)
				}
			}

			// Check for extra keys not in en
			for key := range localeKeys {
				if _, ok := enKeys[key]; !ok {
					t.Errorf("extra key %q in %s.json (not in en.json)", key, loc.Code)
				}
			}
		})
	}
}

func TestLocaleValidJSON(t *testing.T) {
	for _, loc := range SupportedLocales {
		t.Run(loc.Code, func(t *testing.T) {
			data, err := localeFS.ReadFile("locales/" + loc.Code + ".json")
			if err != nil {
				t.Fatalf("failed to read %s.json: %v", loc.Code, err)
			}
			var m map[string]string
			if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
				t.Fatalf("invalid JSON in %s.json: %v", loc.Code, jsonErr)
			}
			if len(m) == 0 {
				t.Errorf("%s.json is empty", loc.Code)
			}
		})
	}
}

func TestTranslationFallback(t *testing.T) {
	initI18n("en")
	if got := tHelper("install.welcome.title"); got == "install.welcome.title" {
		t.Error("expected translated text, got raw key")
	}

	// Unknown locale falls back to en
	initI18n("xx")
	if got := tHelper("install.welcome.title"); got == "install.welcome.title" {
		t.Error("expected English fallback, got raw key")
	}

	// Unknown key returns key itself
	if got := tHelper("nonexistent.key"); got != "nonexistent.key" {
		t.Errorf("expected raw key for unknown, got %q", got)
	}
}

func TestTranslationInterpolation(t *testing.T) {
	initI18n("en")
	result := tHelper("install.port.inUse", "port", "8080")
	if result != "Warning: port 8080 is already in use" {
		t.Errorf("unexpected interpolation result: %q", result)
	}
}

// tHelper wraps t() to avoid name collision with *testing.T
func tHelper(key string, args ...string) string {
	return t(key, args...)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
