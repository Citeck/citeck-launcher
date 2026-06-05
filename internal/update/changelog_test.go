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

// When the running version is not older than the resolved latest (e.g. the user
// is on a pre-release that /releases/latest excludes, so latest resolves to an
// older stable tag), there is nothing to show and no fetch should happen — even
// if the index would 404.
func TestChangelogNoUpdateSkipsFetch(t *testing.T) {
	fetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = true
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := &client{http: http.DefaultClient, rawBase: srv.URL, repo: "Citeck/citeck-launcher"}
	notes, err := changelog(context.Background(), c, "2.5.0", Latest{Tag: "v2.3.2", Version: "2.3.2"}, "ru")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected empty notes, got %+v", notes)
	}
	if fetched {
		t.Fatal("expected no raw fetch when there is no update")
	}
}

// An update IS available but its tag predates changelog/index.json → the index
// 404s. That must degrade to an empty changelog, not a surfaced error.
func TestChangelogMissingIndexIsGraceful(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // index.json (and everything else) 404s
	}))
	defer srv.Close()

	c := &client{http: http.DefaultClient, rawBase: srv.URL, repo: "Citeck/citeck-launcher"}
	notes, err := changelog(context.Background(), c, "2.4.0", Latest{Tag: "v2.6.0", Version: "2.6.0"}, "ru")
	if err != nil {
		t.Fatalf("expected graceful empty on 404 index, got %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected empty notes, got %+v", notes)
	}
}
