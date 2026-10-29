package bundle

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type localizedHolder struct {
	Title LocalizedText `yaml:"title" json:"title"`
}

func decodeLocalized(t *testing.T, yml string) LocalizedText {
	t.Helper()
	var h localizedHolder
	require.NoError(t, yaml.Unmarshal([]byte(yml), &h))
	return h.Title
}

// A scalar is one sentence for every reader; a mapping is one per language,
// keyed by the language alone (ru-RU, ru_RU and RU are ru); anything else, and
// every entry that is not a scalar, is dropped without an error.
func TestLocalizedText_UnmarshalYAMLIsTotal(t *testing.T) {
	assert.Equal(t, PlainText("Document scanning"), decodeLocalized(t, "title: '  Document scanning  '"))
	assert.Equal(t, LocalizedText{"ru": "Сканирование", "en": "Scanning"},
		decodeLocalized(t, "title:\n  ru-RU: Сканирование\n  EN: Scanning\n  de: \n  fr: [a, b]\n"))
	assert.Nil(t, decodeLocalized(t, "title:"))
	assert.Nil(t, decodeLocalized(t, "title: ''"))
	assert.Nil(t, decodeLocalized(t, "title: [a, b]"))
	assert.Nil(t, decodeLocalized(t, "title:\n  ru: [a]\n"))
	assert.True(t, decodeLocalized(t, "title: [a, b]").IsEmpty())
}

// The reader gets their language, else English, else the first language by
// key — never a map-order lottery; the scalar form answers every reader.
func TestLocalizedText_Resolve(t *testing.T) {
	ml := LocalizedText{"ru": "Сканирование", "en": "Scanning", "de": "Scannen"}
	assert.Equal(t, "Сканирование", ml.Resolve("ru"))
	assert.Equal(t, "Сканирование", ml.Resolve("ru-RU"))
	assert.Equal(t, "Scanning", ml.Resolve("fr"), "English before any other language")
	assert.Equal(t, "Scanning", ml.Resolve(""))
	assert.Equal(t, "Scannen", LocalizedText{"ru": "Сканирование", "de": "Scannen"}.Resolve("fr"),
		"no English: the first language by key")
	assert.Equal(t, "Scanning", PlainText("Scanning").Resolve("ru"))
	assert.Empty(t, LocalizedText(nil).Resolve("ru"))
	assert.Empty(t, PlainText("   ").Resolve("ru"))
}

// Round trips keep the author's form: a scalar stays a scalar, a map a map.
func TestLocalizedText_RoundTrips(t *testing.T) {
	for _, in := range []string{"title: Scanning\n", "title:\n  en: Scanning\n  ru: Сканирование\n"} {
		var h localizedHolder
		require.NoError(t, yaml.Unmarshal([]byte(in), &h))
		out, err := yaml.Marshal(h)
		require.NoError(t, err)
		var back localizedHolder
		require.NoError(t, yaml.Unmarshal(out, &back))
		assert.Equal(t, h, back, in)
	}
	for _, in := range []string{`{"title":"Scanning"}`, `{"title":{"en":"Scanning","ru":"Сканирование"}}`, `{"title":null}`, `{"title":7}`} {
		var h localizedHolder
		require.NoError(t, json.Unmarshal([]byte(in), &h), in)
		out, err := json.Marshal(h)
		require.NoError(t, err)
		var back localizedHolder
		require.NoError(t, json.Unmarshal(out, &back))
		assert.Equal(t, h, back, in)
	}
	assert.JSONEq(t, `{"title":"Scanning"}`, mustJSON(t, localizedHolder{Title: PlainText("Scanning")}))
	assert.JSONEq(t, `{"title":null}`, mustJSON(t, localizedHolder{}))
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
