// Package msg carries an operator-facing sentence as DATA rather than as a
// rendered string.
//
// The launcher builds these sentences deep inside pure packages (the migration
// preflights, the dependency edit gate) and shows them on two surfaces in eight
// languages. Building the string there would decide the language at the point
// that knows least about the reader: internal/deps/migrate has no locale, no
// request and no business acquiring one, and a daemon is a long-lived process
// serving a desktop UI in one language and a CLI in another at the same time.
//
// So the builders return a Message — a locale key plus its arguments — and the
// LOCALE is applied at the edge, where the reader is known (see
// i18n.Translator.Render and the daemon's per-request translator). The wire
// contract is unchanged by this: what leaves the daemon is still a plain,
// already-rendered string, so neither the web UI nor the CLI has to learn a
// second rendering path, and the sentence itself lives in exactly one place —
// internal/i18n/locales/*.json — instead of being maintained twice.
//
// This package is a LEAF on purpose: it imports nothing, so the pure packages
// can produce Messages without taking on i18n's embedded locale files or the
// daemon config those load from.
package msg

// Message is one sentence: the locale key, and the arguments to interpolate.
//
// Args is a flat key/value sequence ("volume", "postgres2", "image", "…"),
// matching i18n.T's variadic form so no conversion is needed at the edge. An
// odd-length Args is a programming error and renders the trailing key with an
// empty value rather than panicking — a half-rendered sentence is a bug report,
// a crashed daemon is an outage.
type Message struct {
	Key  string
	Args []string
}

// New builds a Message. The args are the same "k", "v", "k", "v" pairs T takes.
func New(key string, args ...string) Message {
	return Message{Key: key, Args: args}
}

// Empty reports whether this is the zero Message — no key, nothing to render.
// Callers use it to tell "no sentence" from "a sentence that renders empty",
// which is why the zero value has to remain meaningful.
func (m Message) Empty() bool { return m.Key == "" }
