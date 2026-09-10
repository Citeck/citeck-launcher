package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// The CLI states its language on every request, and every request means both
// transports: the ordinary one and the SSE stream `citeck deps upgrade`
// follows a migration on.
//
// Without it the daemon renders its own sentences — the preflight problems,
// the refusals, the per-step progress — in whatever daemon.yml configured,
// which on a desktop is the UI's language and not the terminal's. Nothing
// fails when the header is missing; the operator just reads the wrong
// language, which is exactly why this needs a test.
func TestEveryRequestStatesTheCLILocale(t *testing.T) {
	i18n.ResetForTest()
	t.Cleanup(i18n.ResetForTest)
	i18n.InitI18n("de")

	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(api.LocaleHeader)
		if r.URL.Path == api.Events {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.GetDependencies(); err != nil {
		t.Fatal(err)
	}
	if h := <-got; h != "de" {
		t.Errorf("GET: locale header = %q, want %q", h, "de")
	}

	if _, err := c.StreamEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h := <-got; h != "de" {
		t.Errorf("SSE: locale header = %q, want %q", h, "de")
	}
}

// Before the CLI has resolved a language there is nothing truthful to claim,
// and the honest way to say "you decide" is to send no header at all — which
// makes the daemon fall back to its own configured locale. Sending "en" there
// would silently override a server-mode daemon.yml for every internal call.
func TestNoLocaleIsStatedBeforeOneIsResolved(t *testing.T) {
	i18n.ResetForTest()
	t.Cleanup(i18n.ResetForTest)

	got := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Values(api.LocaleHeader)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).GetDependencies(); err != nil {
		t.Fatal(err)
	}
	if h := <-got; len(h) != 0 {
		t.Errorf("locale header = %v, want it absent", h)
	}
}
