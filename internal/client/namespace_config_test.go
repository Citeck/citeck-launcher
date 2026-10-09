package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// `citeck setup content-storage` writes the namespace config through the
// daemon's single write path. The body must arrive verbatim at PUT /config,
// and the request must state the CLI's language: the write gate's refusal is a
// sentence the daemon renders per request, and before this the raw PUTs were
// the one kind of request that did not say which language to render it in.
func TestPutNamespaceConfigSendsTheBodyVerbatimInTheCLILocale(t *testing.T) {
	i18n.ResetForTest()
	t.Cleanup(i18n.ResetForTest)
	i18n.InitI18n("ru")

	type seen struct{ method, path, locale, contentType, body string }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.Path, r.Header.Get(api.LocaleHeader), r.Header.Get("Content-Type"), string(b)}
		_, _ = w.Write([]byte(`{"success":true,"message":"Configuration saved and applied"}`))
	}))
	defer srv.Close()

	body := "id: default\ncontentStorage:\n  type: S3\n"
	res, err := newTestClient(srv).PutNamespaceConfig([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	s := <-got
	if s.method != http.MethodPut || s.path != api.Config {
		t.Errorf("request = %s %s, want PUT %s", s.method, s.path, api.Config)
	}
	if s.locale != "ru" {
		t.Errorf("locale header = %q, want %q", s.locale, "ru")
	}
	if s.contentType != "text/yaml" {
		t.Errorf("content type = %q, want text/yaml", s.contentType)
	}
	if s.body != body {
		t.Errorf("body = %q, want it verbatim", s.body)
	}
	if res == nil || !res.Success || res.Message != "Configuration saved and applied" {
		t.Errorf("result = %+v", res)
	}
}

// A refusal comes back as *APIError carrying the daemon's own sentence, so the
// CLI can print it as is.
func TestPutNamespaceConfigRefusalIsAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"Conflict","code":"` + api.ErrCodeContentStorageDowngrade + `","message":"cannot go back"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).PutNamespaceConfig([]byte("id: default\n"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Message != "cannot go back" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestGetContentStorageOptionsUsesExplicitBundle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != api.ContentStorageOptions {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("bundleRepo") != "enterprise + repo" || r.URL.Query().Get("bundleKey") != "LATEST" {
			t.Errorf("query = %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"s3":{"available":true,"providerApp":"seaweedfs"}}`))
	}))
	defer srv.Close()
	options, err := newTestClient(srv).GetContentStorageOptions("enterprise + repo", "LATEST")
	if err != nil || options == nil || !options.S3.Available || options.S3.ProviderApp != "seaweedfs" {
		t.Fatalf("options = %+v, err = %v", options, err)
	}
}

func TestExportSnapshotSecretsOptIn(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(strconv.FormatBool(include), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					IncludeSecrets bool `json:"includeSecrets"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.IncludeSecrets != include {
					t.Error("wrong secret export policy")
				}
				if r.URL.Query().Get("output") != "/snapshot dir" {
					t.Error("output path changed")
				}
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer srv.Close()
			var err error
			if include {
				_, err = newTestClient(srv).ExportSnapshot("/snapshot dir", true)
			} else {
				_, err = newTestClient(srv).ExportSnapshot("/snapshot dir")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
