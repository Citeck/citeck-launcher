package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLatestFromRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Citeck/citeck-launcher/releases/latest" {
			http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := &client{http: noRedirect(), githubBase: srv.URL, repo: "Citeck/citeck-launcher"}
	tag, err := c.resolveLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v2.6.0" {
		t.Fatalf("tag = %q want v2.6.0", tag)
	}
}

func TestFetchRawAndDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Citeck/citeck-launcher/v2.6.0/changelog/index.json" {
			_, _ = w.Write([]byte(`[{"version":"2.6.0","date":"2026-06-01"}]`))
			return
		}
		if r.URL.Path == "/asset.bin" {
			_, _ = w.Write([]byte("payload-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := &client{http: http.DefaultClient, rawBase: srv.URL, repo: "Citeck/citeck-launcher"}
	body, err := c.fetchRaw(context.Background(), "v2.6.0", "changelog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `[{"version":"2.6.0","date":"2026-06-01"}]` {
		t.Fatalf("fetchRaw body = %q", body)
	}

	dst := filepath.Join(t.TempDir(), "out.bin")
	if err := c.downloadFile(context.Background(), srv.URL+"/asset.bin", dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "payload-bytes" {
		t.Fatalf("downloaded = %q", got)
	}
}
