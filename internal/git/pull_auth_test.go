package git

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLocalUpstream creates a bare-ish upstream repo on disk with one commit
// on branch "master" so it can be cloned via a plain filesystem path.
func newLocalUpstream(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "upstream")
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o600))
	_, err = wt.Add("file.txt")
	require.NoError(t, err)
	_, err = wt.Commit("initial", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com"},
	})
	require.NoError(t, err)
	return dir
}

// TestDoPull_AuthErrorDoesNotReclone is the 8b-13 regression test: a 403
// (transport.ErrAuthorizationFailed) on pull must surface as an auth failure
// WITHOUT attempting a reclone — a fresh clone would hit the same credential
// problem and just waste bandwidth.
func TestDoPull_AuthErrorDoesNotReclone(t *testing.T) {
	upstream := newLocalUpstream(t)

	// Clone the upstream locally so we have a valid working repo with an
	// origin remote and a remote-tracking ref.
	dest := filepath.Join(t.TempDir(), "clone")
	_, err := gogit.PlainClone(dest, false, &gogit.CloneOptions{URL: upstream})
	require.NoError(t, err)

	// HTTP server that always answers 403 — go-git maps this to
	// transport.ErrAuthorizationFailed ("authorization failed").
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	// Point origin at the 403 server.
	repo, err := gogit.PlainOpen(dest)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteRemote("origin"))
	_, err = repo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{srv.URL + "/repo.git"},
	})
	require.NoError(t, err)

	err = doPull(context.Background(), RepoOpts{
		URL:     srv.URL + "/repo.git",
		Branch:  "master",
		DestDir: dest,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, transport.ErrAuthorizationFailed)
	assert.Contains(t, err.Error(), "git auth failed")
	assert.NotContains(t, err.Error(), "reclone")

	// Pull performs exactly one ref-discovery request; a reclone attempt
	// would have produced at least one more.
	assert.Equal(t, int64(1), requests.Load(), "auth failure on pull must not trigger reclone requests")

	// The existing clone must be left intact for stale-data fallback.
	assert.DirExists(t, filepath.Join(dest, ".git"))
	assert.FileExists(t, filepath.Join(dest, "file.txt"))
}

// TestIsAuthError covers the sentinel and substring classification paths.
func TestIsAuthError(t *testing.T) {
	assert.False(t, isAuthError(nil))
	assert.True(t, isAuthError(transport.ErrAuthenticationRequired))
	assert.True(t, isAuthError(transport.ErrAuthorizationFailed))
	assert.True(t, isAuthError(errors.New("server said: Unauthorized")))
	assert.True(t, isAuthError(errors.New("authentication required: bad token")))
	assert.False(t, isAuthError(errors.New("connection refused")))
}
