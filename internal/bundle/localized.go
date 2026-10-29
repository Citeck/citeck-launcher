package bundle

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// LocalizedText is an author's sentence for a dialog — a feature's title or
// description — written either ONCE, for every reader, or once per language:
//
//	title: Document scanning
//	title:
//	  ru: Сканирование документов
//	  en: Document scanning
//
// The map form is the platform's own MLText shape, which bundle and workspace
// authors already write; the scalar form keeps the simple case simple, and is
// what every bundle written before the map form existed is read as.
//
// The language is chosen by Resolve at the HTTP boundary, where the reader is
// known (the request's locale — see daemon.translatorFor), never here: inside
// the bundle the text travels as the author wrote it, and the DTO a client
// reads carries one plain string. So neither the web UI nor the CLI grew a
// rendering path for it, the same rule the migration sentences follow.
//
// Parsing is TOTAL, like every other piece of operator configuration this
// package reads: a shape the launcher cannot read costs the text alone, never
// the bundle. The scalar form is stored under the empty key.
type LocalizedText map[string]string

// plainTextKey is where the single-language form is kept.
const plainTextKey = ""

// nullTag is the tag yaml.v3 resolves an empty or `null` scalar to.
const nullTag = "!!null"

// PlainText is the single-language form, for callers that build a Def in code.
func PlainText(s string) LocalizedText {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return LocalizedText{plainTextKey: s}
}

// Resolve picks the sentence for a reader of the given locale: the
// single-language text when the author wrote one, else the exact language,
// then English, then the first language the author wrote (by key, so the
// answer does not depend on map order). Empty when there is nothing to show.
func (t LocalizedText) Resolve(locale string) string {
	if len(t) == 0 {
		return ""
	}
	if s := t[plainTextKey]; s != "" {
		return s
	}
	if loc := normalizeTextLocale(locale); loc != "" {
		if s := t[loc]; s != "" {
			return s
		}
	}
	if s := t["en"]; s != "" {
		return s
	}
	for _, k := range slices.Sorted(maps.Keys(t)) {
		if s := t[k]; s != "" {
			return s
		}
	}
	return ""
}

// IsEmpty reports whether the author wrote nothing readable.
func (t LocalizedText) IsEmpty() bool { return len(t) == 0 }

// normalizeTextLocale reduces a locale to the language the launcher's assets
// are keyed by: "ru-RU", "ru_RU" and "RU" all mean "ru".
func normalizeTextLocale(locale string) string {
	locale = strings.ToLower(strings.TrimSpace(locale))
	locale = strings.ReplaceAll(locale, "_", "-")
	if i := strings.IndexByte(locale, '-'); i >= 0 {
		locale = locale[:i]
	}
	return locale
}

// UnmarshalYAML accepts a scalar or a mapping of language → scalar and never
// returns an error: anything else, and every entry it cannot read, is dropped.
func (t *LocalizedText) UnmarshalYAML(node *yaml.Node) error {
	*t = localizedTextFromNode(node)
	return nil
}

// MarshalYAML writes the single-language form back as the scalar it was read
// from, and the map form as a map.
func (t LocalizedText) MarshalYAML() (any, error) {
	if len(t) == 0 {
		return nil, nil
	}
	if s, plain := t[plainTextKey]; plain && len(t) == 1 {
		return s, nil
	}
	return map[string]string(t), nil
}

// UnmarshalJSON mirrors UnmarshalYAML for the JSON form of a Def: a string or
// an object of language → string, anything else dropped.
func (t *LocalizedText) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*t = PlainText(s)
		return nil
	}
	// Total by design, like the YAML form: a shape that is neither a string
	// nor an object costs the text alone.
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		*t = nil
		return nil
	}
	out := make(LocalizedText, len(m))
	for k, v := range m {
		if loc, text := normalizeTextLocale(k), strings.TrimSpace(v); loc != "" && text != "" {
			out[loc] = text
		}
	}
	if len(out) == 0 {
		out = nil
	}
	*t = out
	return nil
}

// MarshalJSON mirrors MarshalYAML.
func (t LocalizedText) MarshalJSON() ([]byte, error) {
	if len(t) == 0 {
		return []byte("null"), nil
	}
	var v any = map[string]string(t)
	if s, plain := t[plainTextKey]; plain && len(t) == 1 {
		v = s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal localized text: %w", err)
	}
	return b, nil
}

func localizedTextFromNode(node *yaml.Node) LocalizedText {
	node = followAlias(node)
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == nullTag {
			return nil
		}
		return PlainText(node.Value)
	case yaml.MappingNode:
		out := make(LocalizedText)
		for _, e := range mappingEntries(node) {
			v := followAlias(e.node)
			if v == nil || v.Kind != yaml.ScalarNode || v.Tag == nullTag {
				continue
			}
			if loc, text := normalizeTextLocale(e.key), strings.TrimSpace(v.Value); loc != "" && text != "" {
				out[loc] = text
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
