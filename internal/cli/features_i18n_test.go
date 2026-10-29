package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// featuresKeyPrefixes are the CLI-asset keys of the experimental-features
// feature: the `citeck features` lines, the wizard step and the help texts.
var featuresKeyPrefixes = []string{"features.", "install.experimental.", "help.features."}

// featuresIdenticalOK are the values that legitimately read the same in every
// language: a pure format string.
var featuresIdenticalOK = map[string]bool{"features.descriptionLine": true}

// Same two checks as the content-storage sentences: a translation that lost a
// placeholder prints "{id}" to the operator, and an English value left in
// another locale passes the key-parity test in silence.
func TestFeaturesLocalesKeepPlaceholdersAndAreTranslated(t *testing.T) {
	en := loadLocale(t, "en")
	var keys []string
	for k := range en {
		for _, p := range featuresKeyPrefixes {
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
			if strings.Count(en[k], " ") > 0 && !featuresIdenticalOK[k] {
				assert.NotEqual(t, en[k], v, "%s: %s is left untranslated", loc.Code, k)
			}
		}
	}
}

// Every key the command renders exists in the asset, so no bare key can leak
// into a table or a sentence.
func TestFeaturesCommandKeysExist(t *testing.T) {
	en := loadLocale(t, "en")
	for _, k := range []string{
		"features.col.id", "features.col.title", "features.col.apps", "features.col.state",
		"features.state.on", "features.state.off", "features.descriptionLine", "features.legend",
		"features.empty", "features.bundleUnresolved", "features.noNamespace", "features.unknown",
		"features.noneOffered", "features.alreadyOn", "features.alreadyOff",
		"features.enabled.running", "features.enabled.stopped", "features.disabled.running", "features.disabled.stopped",
		"install.experimental.label", "install.experimental.hint", "install.experimental.none", "hint.key.mark",
		"help.features.short", "help.features.list.short", "help.features.enable.short", "help.features.disable.short",
	} {
		assert.Contains(t, en, k)
	}
}
