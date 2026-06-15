package bundle

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUnauthorizedGitServer serves 401 to every request — go-git's smart-HTTP
// transport classifies that as transport.ErrAuthenticationRequired, the exact
// failure a TOKEN-auth workspace without a usable token produces.
func newUnauthorizedGitServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestResolveWorkspace_RecordsAuthSyncError: a CUSTOM workspace repo that 401s
// with no cached config must NOT be silently swallowed — ResolveWorkspaceOnly
// still degrades to the empty config (startup resilience), but
// WorkspaceSyncError surfaces the failure with the repo URL and the git
// "authentication required" wording the daemon/Web UI match on.
func TestResolveWorkspace_RecordsAuthSyncError(t *testing.T) {
	srv := newUnauthorizedGitServer(t)
	repoURL := srv.URL + "/citeck/private-ws.git"

	r := NewResolver(t.TempDir()).WithWorkspaceRepo(WorkspaceRepoOpts{URL: repoURL})
	cfg := r.ResolveWorkspaceOnly()

	require.NotNil(t, cfg, "empty-config fallback keeps the daemon booting")
	assert.Empty(t, cfg.BundleRepos, "no real workspace data was loaded")

	err := r.WorkspaceSyncError()
	require.Error(t, err, "auth failure with no cache must be recorded, not swallowed")
	assert.Contains(t, err.Error(), repoURL)
	assert.Contains(t, err.Error(), "authentication required")
}

// TestResolveWorkspace_CachedConfigSuppressesSyncError: when a previously
// cloned workspace config exists on disk, a failed pull stays graceful — the
// stale config is served and no error is surfaced (matches the bundle
// cached-fallback philosophy).
func TestResolveWorkspace_CachedConfigSuppressesSyncError(t *testing.T) {
	srv := newUnauthorizedGitServer(t)
	dataDir := t.TempDir()

	wsRepoDir := filepath.Join(dataDir, "bundles", "workspace")
	require.NoError(t, os.MkdirAll(wsRepoDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wsRepoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n"), 0o644))

	r := NewResolver(dataDir).WithWorkspaceRepo(WorkspaceRepoOpts{URL: srv.URL + "/citeck/ws.git"})
	cfg := r.ResolveWorkspaceOnly()

	require.NotNil(t, cfg)
	require.Len(t, cfg.BundleRepos, 1, "cached config must be served")
	assert.NoError(t, r.WorkspaceSyncError(), "usable cached config suppresses the sync error")
}

// TestResolveWorkspace_OfflineRecordsNoError: offline mode skips git entirely
// — no sync is attempted, so no sync error may be recorded (offline behavior
// unchanged).
func TestResolveWorkspace_OfflineRecordsNoError(t *testing.T) {
	r := NewResolver(t.TempDir()).WithWorkspaceRepo(WorkspaceRepoOpts{URL: "https://unreachable.example.com/ws.git"})
	r.SetOffline(true)

	cfg := r.ResolveWorkspaceOnly()
	require.NotNil(t, cfg)
	require.NoError(t, r.WorkspaceSyncError())
}

// TestWorkspaceSyncError_DefaultRepoStaysGraceful pins the accessor contract:
// the recorded error is surfaced ONLY for custom workspace repo URLs — the
// default Citeck repo failing keeps the historical silent empty-config
// fallback (server bootstrap resilience).
func TestWorkspaceSyncError_DefaultRepoStaysGraceful(t *testing.T) {
	recorded := errors.New("sync workspace repo X: authentication required")

	// No WithWorkspaceRepo at all → default repo → nil.
	r := NewResolver(t.TempDir())
	r.wsSyncErr = recorded
	require.NoError(t, r.WorkspaceSyncError())

	// WithWorkspaceRepo with an empty URL (branch-only override) → still the
	// default repo → nil.
	r2 := NewResolver(t.TempDir()).WithWorkspaceRepo(WorkspaceRepoOpts{Branch: "develop"})
	r2.wsSyncErr = recorded
	require.NoError(t, r2.WorkspaceSyncError())

	// Custom URL → surfaced.
	r3 := NewResolver(t.TempDir()).WithWorkspaceRepo(WorkspaceRepoOpts{URL: "https://gitlab.example.com/ws.git"})
	r3.wsSyncErr = recorded
	assert.Equal(t, recorded, r3.WorkspaceSyncError())
}

// TestResolveWorkspace_SuccessClearsPreviousError: each resolveWorkspace pass
// reflects only its own outcome — a later success (here: a usable on-disk
// config appearing) clears an earlier recorded failure.
func TestResolveWorkspace_SuccessClearsPreviousError(t *testing.T) {
	srv := newUnauthorizedGitServer(t)
	dataDir := t.TempDir()
	r := NewResolver(dataDir).WithWorkspaceRepo(WorkspaceRepoOpts{URL: srv.URL + "/citeck/ws.git"})

	r.ResolveWorkspaceOnly()
	require.Error(t, r.WorkspaceSyncError(), "first pass: failure recorded")

	// A config materializes (e.g. offline ZIP import into repo/).
	repoDir := filepath.Join(dataDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(repoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n"), 0o644))

	cfg := r.ResolveWorkspaceOnly()
	require.Len(t, cfg.BundleRepos, 1)
	assert.NoError(t, r.WorkspaceSyncError(), "second pass loaded a config — error cleared")
}
