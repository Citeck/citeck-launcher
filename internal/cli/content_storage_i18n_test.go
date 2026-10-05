package cli

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// contentStorageKeyPrefixes are the CLI-asset keys of the content-storage
// feature: the daemon's refusals and the CLI's own lines.
var contentStorageKeyPrefixes = []string{
	"contentStorage.",
	"install.contentStorage.",
	"cli.contentStorage.",
	"setup.content_storage.",
	"setup.history.undo.content_storage",
}

var placeholderRe = regexp.MustCompile(`\{[a-zA-Z]+\}`)

func loadLocale(t *testing.T, code string) map[string]string {
	t.Helper()
	data, err := i18n.LocaleFS.ReadFile("locales/" + code + ".json")
	require.NoError(t, err)
	var m map[string]string
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func placeholders(s string) []string {
	got := placeholderRe.FindAllString(s, -1)
	slices.Sort(got)
	return slices.Compact(got)
}

// TestLocaleCompleteness checks that the keys exist; for the CLI asset nothing
// checks the VALUES. For this feature's sentences two mistakes would pass it
// silently: a translation that lost a placeholder (the operator reads "{app}"
// or nothing where the provider's name belongs) and an English value left in
// another locale. Both are checked here.
func TestContentStorageLocalesKeepPlaceholdersAndAreTranslated(t *testing.T) {
	en := loadLocale(t, "en")
	var keys []string
	for k := range en {
		for _, p := range contentStorageKeyPrefixes {
			if strings.HasPrefix(k, p) {
				keys = append(keys, k)
				break
			}
		}
	}
	require.NotEmpty(t, keys)
	for _, loc := range SupportedLocales {
		if loc.Code == "en" {
			continue
		}
		m := loadLocale(t, loc.Code)
		for _, k := range keys {
			v, ok := m[k]
			if !assert.True(t, ok, "%s: missing %s", loc.Code, k) {
				continue
			}
			assert.Equal(t, placeholders(en[k]), placeholders(v), "%s: placeholders of %s", loc.Code, k)
			if strings.Count(en[k], " ") > 0 {
				assert.NotEqual(t, en[k], v, "%s: %s is left untranslated", loc.Code, k)
			}
		}
	}
}
