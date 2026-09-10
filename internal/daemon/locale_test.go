package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/config"
)

func daemonWithLocale(loc string) *Daemon {
	return &Daemon{daemonCfg: config.DaemonConfig{Locale: loc}}
}

// The header wins because only the client knows who is reading: a desktop UI in
// Russian and a CLI in German can be asking this daemon at the same moment, and
// daemon.yml has one value for both.
func TestTheRequestedLocaleWinsOverTheConfiguredOne(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
	r.Header.Set(LocaleHeader, "ru")
	assert.Equal(t, "ru", daemonWithLocale("de").translatorFor(r).Locale())
}

// EventSource cannot set a header, and the SSE stream carries the per-step
// migration messages — so the query parameter is not a convenience, it is the
// only way that surface can be localized at all.
func TestTheQueryParamCarriesTheLocaleWhereHeadersCannot(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/events?locale=ja", http.NoBody)
	assert.Equal(t, "ja", daemonWithLocale("de").translatorFor(r).Locale())
}

func TestTheHeaderWinsOverTheQueryParam(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/events?locale=ja", http.NoBody)
	r.Header.Set(LocaleHeader, "ru")
	assert.Equal(t, "ru", daemonWithLocale("de").translatorFor(r).Locale())
}

// Server mode: the operator configured one language for the box, and the CLI
// reads its own strings from that same setting. An unmarked request must get
// the language the CLI would have printed, or `citeck deps` would mix two.
func TestAnUnmarkedRequestGetsTheConfiguredLocale(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
	assert.Equal(t, "de", daemonWithLocale("de").translatorFor(r).Locale())
}

// Nothing configured, nothing asked for.
func TestWithNothingToGoOnItIsEnglish(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
	assert.Equal(t, "en", daemonWithLocale("").translatorFor(r).Locale())
}

// The locale arrives from outside. A bad one is a cosmetic mismatch; refusing
// the request over it would turn that into a broken screen.
func TestAnUnusableLocaleDegradesToEnglish(t *testing.T) {
	for _, bad := range []string{"klingon", "zz-ZZ", "  "} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/x", http.NoBody)
		r.Header.Set(LocaleHeader, bad)
		assert.Equal(t, "en", daemonWithLocale("").translatorFor(r).Locale(), "locale %q", bad)
	}
}
