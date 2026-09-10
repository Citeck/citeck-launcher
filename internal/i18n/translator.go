package i18n

import (
	"strings"
	"sync"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// Translator renders msg.Message in ONE locale, and is safe to use from many
// goroutines at once.
//
// It exists because the package-level T/InitI18n pair deliberately is not:
// those hold one global locale, loaded once, and their doc says so ("CLI
// commands run sequentially on a single goroutine"). That is right for the CLI
// and wrong for the daemon, which serves a desktop UI in one language and a CLI
// in another AT THE SAME TIME — a global locale there would make the answer
// depend on whoever asked last.
//
// The loaded maps are immutable after construction and shared through a cache,
// so a Translator is a pointer to read-only data: no locking on the hot path,
// and eight locales cost eight map loads for the life of the process.
type Translator struct {
	locale   string
	messages map[string]string
	fallback map[string]string
}

// localeCache holds the parsed locale maps. They are never mutated after they
// are put here, which is what makes handing the same map to any number of
// goroutines safe.
var localeCache sync.Map // locale string -> map[string]string

func cachedLocale(locale string) map[string]string {
	if m, ok := localeCache.Load(locale); ok {
		v, _ := m.(map[string]string)
		return v
	}
	loaded := LoadLocale(locale)
	actual, _ := localeCache.LoadOrStore(locale, loaded)
	v, _ := actual.(map[string]string)
	return v
}

// NewTranslator returns a Translator for the locale, falling back to English
// key by key.
//
// That per-key fallback is defense in depth and is NOT reachable while the
// locale files are in parity — TestLocaleCompleteness fails the build if a key
// exists in one file and not another, so a translator for a supported locale
// finds every key in its own map. It stays because the cost of being wrong is
// asymmetric: a key added to en.json alone would otherwise render as the raw
// key for seven languages at runtime, and the guard costs one map lookup that
// only ever happens on that path.
//
// An unknown locale is not an error and must not be: the locale reaches this
// from a request header, i.e. from outside, and the honest answer to "ru-RU",
// "klingon" or a truncated string is the English sentence — not a 400, and
// certainly not a panic. It is normalized (case, and the region is dropped)
// before the lookup, so "RU" and "ru-RU" both find "ru".
func NewTranslator(locale string) *Translator {
	norm := NormalizeLocale(locale)
	return &Translator{locale: norm, messages: cachedLocale(norm), fallback: cachedLocale("en")}
}

// NormalizeLocale maps whatever a client sent onto one of the supported locale
// ids, or "en" when nothing matches.
func NormalizeLocale(locale string) string {
	s := strings.ToLower(strings.TrimSpace(locale))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	for _, l := range SupportedLocales {
		if l.Code == s {
			return s
		}
	}
	return "en"
}

// Locale is the locale this Translator resolved to.
func (t *Translator) Locale() string { return t.locale }

// T renders a key with {param} interpolation, exactly like the package-level T.
func (t *Translator) T(key string, args ...string) string {
	text := t.messages[key]
	if text == "" {
		text = t.fallback[key]
	}
	if text == "" {
		// The key itself, same as T: a missing key must be visible as a
		// missing key, not as an empty line that reads like "nothing to say".
		return key
	}
	for i := 0; i+1 < len(args); i += 2 {
		text = strings.ReplaceAll(text, "{"+args[i]+"}", args[i+1])
	}
	return text
}

// Render turns a Message into the sentence.
//
// The zero Message renders empty, and deliberately relies on T's own rule
// rather than a guard of its own: an unrecognized key renders AS the key, and
// the zero Message's key is "". A separate `if m.Empty()` here would be a
// branch no test can distinguish from its absence.
func (t *Translator) Render(m msg.Message) string {
	return t.T(m.Key, m.Args...)
}

// RenderAll renders a list, preserving order and length.
//
// It returns a non-nil empty slice for an empty input because these lists are
// JSON arrays on the wire that must never marshal as null — the same contract
// PreflightResult already states for Problems and Warnings.
func (t *Translator) RenderAll(ms []msg.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, t.Render(m))
	}
	return out
}
