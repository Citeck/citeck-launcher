package git

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneAndAuthDiagnosticsDoNotExposeURLCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	parsed, parseErr := url.Parse(srv.URL + "/private.git")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	parsed.User = url.UserPassword("fixture-user", "fixture-password-marker")
	parsed.RawQuery = "private=fixture-query-marker"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	err := CloneOrPullWithAuth(context.Background(), RepoOpts{URL: parsed.String(), Branch: "master", DestDir: filepath.Join(t.TempDir(), "clone")})
	if err == nil {
		t.Fatal("fixture unexpectedly cloned")
	}
	for _, text := range []string{logs.String(), err.Error()} {
		if strings.Contains(text, "fixture-password-marker") || strings.Contains(text, "fixture-query-marker") || strings.Contains(text, "fixture-user") {
			t.Fatal("URL credentials escaped Git diagnostic boundary")
		}
	}
	authErr := TestAuth(parsed.String(), "")
	if authErr == nil {
		t.Fatal("fixture unexpectedly authenticated")
	}
	if strings.Contains(authErr.Error(), "fixture-password-marker") || strings.Contains(authErr.Error(), "fixture-query-marker") {
		t.Fatal("URL credentials escaped auth diagnostic boundary")
	}
}
