package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/stretchr/testify/require"
)

func gitRecoveryFixture(t *testing.T) (*Daemon, *http.ServeMux, *httptest.Server, *atomic.Int64) {
	t.Helper()
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(server.Close)
	d, mux := secretsTestMux(t)
	d.activeNs = &activeNamespace{workspaceID: "private"}
	require.NoError(t, d.store.SaveWorkspace(storage.WorkspaceDto{ID: "private", RepoURL: server.URL + "/workspace.git", AuthType: "TOKEN", SecretID: "workspace-token"}))
	for _, id := range []string{"workspace-token", "a-bundle-token", "z-unrelated", "replacement"} {
		require.NoError(t, d.secretService.SaveSecret(storage.Secret{SecretMeta: storage.SecretMeta{ID: id, Name: id, Type: storage.SecretGitToken}, Value: "fixture-credential"}))
	}
	dir := filepath.Join(config.BundlesDataDir("private"), "bundles", "workspace")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	raw := "custom: original\nbundleRepos:\n  - id: private\n    path: releases\n    url: " + server.URL + "/bundles.git\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspace-v1.yml"), []byte(raw), 0o600))
	require.NoError(t, d.persistWorkspaceDelta("private", []byte(raw), []byte(strings.Replace(raw, "custom: original", "custom: preserved", 1))))
	return d, mux, server, &calls
}

func TestGitRecoveryReportsCacheAndOneOperationChoice(t *testing.T) {
	d, mux, srv, calls := gitRecoveryFixture(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate, http.NoBody))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var failure api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &failure))
	require.NotNil(t, failure.Git)
	require.Equal(t, srv.URL+"/workspace.git", failure.Git.RepoURL)
	require.Equal(t, "workspace-token", failure.Git.SecretID)
	require.True(t, failure.Git.CacheAvailable)
	require.Zero(t, failure.Git.CacheUpdatedAt, "file mtime must not invent a successful sync")
	prior := calls.Load()
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate+"?cachedRepo="+srv.URL+"/workspace.git", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, prior, calls.Load(), "cache action must skip only this attempt's network I/O")
	require.Empty(t, d.cachedRepoURLs)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate, http.NoBody))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Greater(t, calls.Load(), prior, "next retry must attempt Git again")
}

func TestGitRecoveryBindingIsRepositoryScopedAndPreservesDelta(t *testing.T) {
	d, mux, srv, _ := gitRecoveryFixture(t)
	rec := httptest.NewRecorder()
	body := `{"repoUrl":"` + srv.URL + `/bundles.git","secretId":"replacement"}`
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/private/git-secret", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	ws, resolver, cfg, err := d.offlineGitContext("private")
	require.NoError(t, err)
	require.Equal(t, "workspace-token", ws.SecretID, "bundle repair must not change workspace auth")
	require.Equal(t, "secret:replacement", cfg.BundleRepos[0].AuthType)
	baseline, _ := resolver.ResolveWorkspaceRaw()
	content, err := workspaceConfigOverlay(d.store, "private")(baseline)
	require.NoError(t, err)
	require.Contains(t, string(content), "custom: preserved")
	require.Equal(t, "fixture-credential", makeTokenLookup(d.secretService)(cfg.BundleRepos[0].AuthType))
	require.NoError(t, d.secretService.DeleteSecret("replacement"))
	require.Empty(t, makeTokenLookup(d.secretService)(cfg.BundleRepos[0].AuthType), "explicit missing binding must not pick another token")
	details := d.gitFailureDetails("private", &bundle.RepoSyncError{URL: srv.URL + "/bundles.git", Err: bytes.ErrTooLarge}, false)
	require.Equal(t, "replacement", details.SecretID, "missing selected reference must remain visible")
}

func TestGitRecoveryRejectsUnrelatedRepositoryAndUnavailableCache(t *testing.T) {
	d, mux, srv, calls := gitRecoveryFixture(t)
	before := calls.Load()
	for _, target := range []string{srv.URL + "/other.git", srv.URL + "/bundles.git", "https://unrelated.example.invalid/x.git"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate+"?cachedRepo="+target, http.NoBody))
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	require.Equal(t, before, calls.Load(), "invalid cache choices must not perform Git requests")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/private/git-secret", strings.NewReader(`{"repoUrl":"https://unrelated.example.invalid/x.git","secretId":"replacement"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	ws, err := d.store.GetWorkspace("private")
	require.NoError(t, err)
	require.Equal(t, "workspace-token", ws.SecretID)
}

func TestCachedUpdateChoiceCannotCrossWorkspaceAndDoesNotLeak(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "same", namespace.NsStatusStopped)
	d.activeNs.workspaceID = "alpha"
	entered := make(chan []string, 2)
	d.reloadExFn = func(_, _, _ bool) error { entered <- append([]string(nil), d.cachedRepoURLs...); return nil }
	d.reloadMu.Lock()
	d.updateAndStartAsync(false, "same", cachedRepoChoice{workspaceID: "alpha", nsID: "same", URL: "https://example.invalid/ws.git"})
	d.activeNs.workspaceID = "beta"
	d.reloadMu.Unlock()
	require.Eventually(t, func() bool { return d.updateLastFailure.Load() != nil }, time.Second, 5*time.Millisecond)
	require.Contains(t, d.updateLastFailure.Load().msg, "workspace changed")
	require.Empty(t, entered)
	d.updateAndStartAsync(false, "same")
	select {
	case used := <-entered:
		require.Empty(t, used)
	case <-time.After(time.Second):
		t.Fatal("plain update did not run")
	}
}

func TestGitCredentialLogsContainReferencesButNotValues(t *testing.T) {
	d, mux, _, _ := gitRecoveryFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	rec := putSecret(t, mux, "workspace-token", `{"value":"fixture-new-private-value"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	ws, err := d.store.GetWorkspace("private")
	require.NoError(t, err)
	_ = buildWorkspaceRepoOpts(*ws, d.secretService)
	_ = makeTokenLookup(d.secretService)("GIT_TOKEN")
	require.Contains(t, logs.String(), "valueChanged=true")
	require.Contains(t, logs.String(), "workspace-token")
	require.NotContains(t, logs.String(), "fixture-new-private-value")
	require.NotContains(t, logs.String(), "fixture-credential")
}

func TestGitRecoveryCarriesMultipleCacheChoicesAndUsesCapturedCredential(t *testing.T) {
	d, _, srv, _ := gitRecoveryFixture(t)
	root := filepath.Join(config.BundlesDataDir("private"), "bundles", "private", "releases")
	require.NoError(t, os.MkdirAll(root, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "1.0.yml"), []byte("{}\n"), 0o600))
	wsURL := srv.URL + "/workspace.git"
	bundleURL := srv.URL + "/bundles.git"
	req := httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate+"?cachedRepo="+wsURL+"&cachedRepo="+bundleURL, http.NoBody)
	choices, err := d.cachedRepoRequest(req, "private")
	require.NoError(t, err)
	require.Equal(t, []string{wsURL, bundleURL}, choices)
	ws, resolver, _, err := d.offlineGitContext("private")
	require.NoError(t, err)
	// Force both refreshes: only explicitly chosen URLs can suppress the attempts.
	resolver.SetOffline(false)
	_, err = resolver.WithForcePull().WithCachedRepos(choices).Resolve(bundle.Ref{Repo: "private", Key: "1.0"})
	require.NoError(t, err)
	require.NoError(t, resolver.GitSyncError())
	captured := "old-selected-reference"
	detail := d.gitFailureDetails("private", &bundle.RepoSyncError{URL: wsURL, SecretID: &captured, Err: bytes.ErrTooLarge}, true, choices)
	require.Equal(t, captured, detail.SecretID, "a later secret change must not rewrite the failed attempt's identity")
	require.Equal(t, choices, detail.CachedRepos)
	require.True(t, detail.Force)
	// Do not leak a different operation's cache policy into a synchronous error.
	d.cachedRepoURLs = choices
	isolated := d.gitFailureDetails(ws.ID, &bundle.RepoSyncError{URL: wsURL, Err: bytes.ErrTooLarge}, false)
	require.Empty(t, isolated.CachedRepos)
}

func TestGitRecoveryDoesNotOfferAnotherRepositoryAsCache(t *testing.T) {
	d, mux, srv, _ := gitRecoveryFixture(t)
	dir := filepath.Join(config.BundlesDataDir("private"), "bundles", "workspace")
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"https://example.invalid/other.git"}})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate, http.NoBody))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Git)
	require.False(t, body.Git.CacheAvailable)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.WorkspaceUpdate+"?cachedRepo="+srv.URL+"/workspace.git", http.NoBody))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, d.cachedRepoURLs)
}
