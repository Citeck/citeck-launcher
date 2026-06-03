package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChangelogRangeAndFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Citeck/citeck-launcher/v2.6.0/changelog/index.json":
			_, _ = w.Write([]byte(`[
				{"version":"2.4.0","date":"2026-01-01"},
				{"version":"2.5.0","date":"2026-03-01"},
				{"version":"2.6.0","date":"2026-06-01"}
			]`))
		case "/Citeck/citeck-launcher/v2.6.0/changelog/2.6.0/ru.md":
			_, _ = w.Write([]byte("# 2.6.0\n- новое"))
		case "/Citeck/citeck-launcher/v2.6.0/changelog/2.5.0/ru.md":
			http.NotFound(w, r) // force en fallback
		case "/Citeck/citeck-launcher/v2.6.0/changelog/2.5.0/en.md":
			_, _ = w.Write([]byte("# 2.5.0\n- new"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &client{http: http.DefaultClient, rawBase: srv.URL, repo: "Citeck/citeck-launcher"}
	notes, err := changelog(context.Background(), c, "2.4.0", Latest{Tag: "v2.6.0", Version: "2.6.0"}, "ru")
	if err != nil {
		t.Fatal(err)
	}
	// Range (2.4.0, 2.6.0] = {2.6.0, 2.5.0}; excludes the current 2.4.0; newest first.
	if len(notes) != 2 {
		t.Fatalf("len(notes)=%d want 2: %+v", len(notes), notes)
	}
	if notes[0].Version != "2.6.0" || notes[1].Version != "2.5.0" {
		t.Fatalf("order = %s,%s want 2.6.0,2.5.0", notes[0].Version, notes[1].Version)
	}
	if notes[0].Markdown != "# 2.6.0\n- новое" {
		t.Fatalf("ru note = %q", notes[0].Markdown)
	}
	if notes[1].Markdown != "# 2.5.0\n- new" { // en fallback
		t.Fatalf("fallback note = %q", notes[1].Markdown)
	}
}
