package update

import (
	"context"
	"errors"
	"github.com/citeck/citeck-launcher/internal/downloadhttp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestResolveLatestNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 200, no Location header
	}))
	defer srv.Close()

	c := &client{http: noRedirect(), githubBase: srv.URL, repo: "Citeck/citeck-launcher"}
	if _, err := c.resolveLatest(context.Background()); err == nil {
		t.Fatal("resolveLatest must error when there is no redirect Location")
	}
}

func TestResolveLatestStripsQueryFragment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0?x=1#notes", http.StatusFound)
	}))
	defer srv.Close()

	c := &client{http: noRedirect(), githubBase: srv.URL, repo: "Citeck/citeck-launcher"}
	tag, err := c.resolveLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v2.6.0" {
		t.Fatalf("tag = %q want v2.6.0 (query/fragment stripped)", tag)
	}
}

func TestFetchRawNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c := &client{http: http.DefaultClient, rawBase: srv.URL, repo: "Citeck/citeck-launcher"}
	if _, err := c.fetchRaw(context.Background(), "v2.6.0", "changelog/missing.json"); err == nil {
		t.Fatal("fetchRaw must error on non-2xx")
	}
}

func TestDownloadFileContinuesBeyondThirtySeconds(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 17 {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}))
	defer srv.Close()
	c := &client{http: NewService("2.16.2", t.TempDir()).http}
	dst := filepath.Join(t.TempDir(), "payload")
	if err := c.downloadFile(context.Background(), srv.URL, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "xxxxxxxxxxxxxxxxx" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestDownloadFileIdleTimeoutPreservesDestination(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := &client{http: NewService("2.16.2", t.TempDir()).http}
	dst := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := defaultDownloadPolicy()
	policy.stalled = 1
	err := c.downloadFileWithPolicy(context.Background(), srv.URL, dst, policy)
	if !errors.Is(err, downloadhttp.ErrIdleTimeout) {
		t.Fatalf("got %v", err)
	}
	data, readErr := os.ReadFile(dst)
	if readErr != nil || string(data) != "original" {
		t.Fatalf("data=%q err=%v", data, readErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(dst))
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("temporary download leaked: %v, %v", entries, readErr)
	}
}
