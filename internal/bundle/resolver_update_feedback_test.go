package bundle

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/stretchr/testify/require"
)

func TestExplicitBundleRefreshReportsFailureOverCachedConfig(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate=%v", separate), func(t *testing.T) {
			srv := newUnauthorizedGitServer(t)
			data := t.TempDir()
			workspace := filepath.Join(data, "bundles", "workspace")
			require.NoError(t, os.MkdirAll(filepath.Join(workspace, "releases"), 0o750))
			raw := "bundleRepos:\n  - id: private\n    path: releases\n"
			if separate {
				raw = "bundleRepos:\n  - id: private\n    url: " + srv.URL + "/bundles.git\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "workspace-v1.yml"), []byte(raw), 0o600))
			r := NewResolver(data).WithWorkspaceRepo(WorkspaceRepoOpts{URL: srv.URL + "/workspace.git"}).WithForcePull()
			cfg := r.ResolveWorkspaceOnly()
			require.Len(t, cfg.BundleRepos, 1)
			require.NoError(t, r.WorkspaceSyncError(), "cached config stays available to startup")
			_, err := r.SyncBundleRepo(cfg, "private")
			require.ErrorContains(t, err, "authentication required", "explicit refresh must not claim success")
		})
	}
}

func TestCachedBundleResolutionRecordsGitFailureButOfflineRemainsUsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(srv.Close)
	data := t.TempDir()
	manual := filepath.Join(data, "repo")
	cached := filepath.Join(data, "bundles", "private", "releases")
	require.NoError(t, os.MkdirAll(manual, 0o750))
	require.NoError(t, os.MkdirAll(cached, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(manual, "workspace-v1.yml"), []byte("bundleRepos:\n  - id: private\n    url: "+srv.URL+"/bundles.git\n    path: releases\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cached, "1.0.yml"), []byte("{}\n"), 0o600))
	r := NewResolver(data).WithForcePull()
	res, err := r.Resolve(Ref{Repo: "private", Key: "1.0"})
	require.NoError(t, err, "cached resolution remains available")
	require.NotNil(t, res)
	require.ErrorContains(t, r.GitSyncError(), "authentication required")
	r.SetOffline(true)
	_, err = r.Resolve(Ref{Repo: "private", Key: "1.0"})
	require.NoError(t, err)
	require.NoError(t, r.GitSyncError(), "skipped offline sync must not retain last attempt's error")
}

func TestCachedRepoIdentityDoesNotFollowChangedConfiguration(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"https://example.invalid/original.git"}})
	require.NoError(t, err)
	require.True(t, CachedRepoMatches(dir, "https://example.invalid/original.git"))
	require.False(t, CachedRepoMatches(dir, "https://example.invalid/replacement.git"))
	require.True(t, CachedRepoMatches(t.TempDir(), "https://example.invalid/manual.git"))
}

func TestLegacyOriginWarningDoesNotExposeURLCredentials(t *testing.T) {
	dir := t.TempDir()
	repo, initErr := gogit.PlainInit(dir, false)
	require.NoError(t, initErr)
	_, remoteErr := repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"https://fixture-user:fixture-password-marker@example.invalid/old.git?private=fixture-query-marker"}})
	require.NoError(t, remoteErr)
	var logs bytes.Buffer
	resolver := NewResolver(t.TempDir()).WithLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	resolver.warnOnLegacyRepoOriginMismatch(dir, "https://another-user:another-password-marker@example.invalid/new.git?private=another-query-marker")
	text := logs.String()
	require.Contains(t, text, "different git remote")
	for _, marker := range []string{"fixture-user", "fixture-password-marker", "fixture-query-marker", "another-user", "another-password-marker", "another-query-marker"} {
		if strings.Contains(text, marker) {
			t.Fatal("legacy origin warning exposed URL credentials")
		}
	}
	require.Contains(t, text, "https://example.invalid/old.git")
	require.Contains(t, text, "https://example.invalid/new.git")
}
