package daemon

import (
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// LocaleHeader is how a client states which language it wants its sentences in,
// and LocaleQueryParam is the same thing for connections that cannot carry
// headers — EventSource has no way to set one, so the SSE stream, which carries
// the per-step migration messages, states its locale in the URL instead. One
// name, two transports; nothing else may grow a third.
//
// Both are ALIASES of the api constants rather than literals, because the
// spelling is needed on both sides of the wire: internal/client writes them and
// this package reads them. A second literal over there is exactly how "the CLI
// stopped being translated" becomes a silent regression — nothing fails when a
// header nobody recognizes is sent.
//
// Deliberately not Accept-Language: the browser sends that one on its own, with
// the OS language, which is NOT the language the operator picked in the launcher
// (the web UI keeps that in localStorage). Honoring Accept-Language would answer
// a question nobody asked, and would do it silently.
const (
	LocaleHeader     = api.LocaleHeader
	LocaleQueryParam = api.LocaleQueryParam
)

// translatorFor resolves the language for ONE request.
//
// Order, and the reason for it: what the client asked for wins, because only
// the client knows who is reading — a desktop UI in Russian and a CLI in German
// can be talking to this daemon at the same moment. Then daemon.yml's locale,
// which is the server-mode answer: there the operator configured one language
// for the box and the CLI reads its own strings from the same setting, so an
// unmarked request gets the same language the CLI would have printed. Then
// English.
//
// Anything unrecognizable degrades to English rather than failing the request
// (see i18n.NormalizeLocale): the sentence is the point, the language is a
// preference, and a 400 for a bad locale would turn a cosmetic mismatch into a
// broken screen.
func (d *Daemon) translatorFor(r *http.Request) *i18n.Translator {
	if r != nil {
		if v := r.Header.Get(LocaleHeader); v != "" {
			return i18n.NewTranslator(v)
		}
		if v := r.URL.Query().Get(LocaleQueryParam); v != "" {
			return i18n.NewTranslator(v)
		}
	}
	return i18n.NewTranslator(d.daemonCfg.Locale)
}
