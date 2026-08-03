package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLocalUpstreamNamed builds a local upstream repo on branch "master" whose
// single commit contains `marker` as a file, so a clone of it is trivially
// identifiable on disk. Mirrors newLocalUpstream (pull_auth_test.go) but lets a
// test create two DISTINCT upstreams and tell them apart.
func newLocalUpstreamNamed(t *testing.T, marker string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "upstream-"+marker)
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, marker+".txt"), []byte(marker+"\n"), 0o600))
	_, err = wt.Add(marker + ".txt")
	require.NoError(t, err)
	_, err = wt.Commit("initial "+marker, &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com"},
	})
	require.NoError(t, err)
	return dir
}

// cloneLocal makes a working clone of `upstream` with an "origin" remote — the
// on-disk state doPull/reclone operate on.
func cloneLocal(t *testing.T, upstream string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "clone")
	_, err := gogit.PlainClone(dest, false, &gogit.CloneOptions{URL: upstream})
	require.NoError(t, err)
	return dest
}

// snapshotTree fingerprints every regular file under root (path -> sha256) so a
// test can assert a directory is byte-for-byte untouched.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("rel %s: %w", path, relErr)
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	require.NoError(t, err)
	return out
}

// deadServerURL starts and immediately stops an HTTP server, yielding a URL
// that is guaranteed to refuse connections — a deterministic transient network
// failure with no sleeps or flaky timeouts.
func deadServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	u := srv.URL
	srv.Close()
	return u + "/repo.git"
}

// setOrigin repoints the clone's origin remote at `url`.
func setOrigin(t *testing.T, dest, url string) {
	t.Helper()
	repo, err := gogit.PlainOpen(dest)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteRemote("origin"))
	_, err = repo.CreateRemote(&gogitconfig.RemoteConfig{Name: "origin", URLs: []string{url}})
	require.NoError(t, err)
}

// TestIsTransientNetworkError is the classification table for the new
// transient-vs-fatal split. A misclassification in either direction is a data
// hazard: "transient" reported as fatal destroys a good clone (the reported
// bug), "auth" reported as transient re-tries a hopeless credential forever.
func TestIsTransientNetworkError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"wrapped deadline", fmt.Errorf("pull: %w", context.DeadlineExceeded), true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"eof", io.EOF, true},
		{"net.OpError", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}, true},
		{"wrapped net.OpError", fmt.Errorf("pull: %w", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("boom")}), true},
		{"net.DNSError", &net.DNSError{Err: "no such host", Name: "gitlab.example.com", IsNotFound: true}, true},
		{"timeout net.Error", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, true},

		// String fallbacks — go-git flattens most transport failures into
		// opaque error strings, so these are the realistic shapes.
		{"tls handshake timeout", errors.New(`Get "https://gitlab.citeck.ru/x/info/refs?service=git-upload-pack": net/http: TLS handshake timeout`), true},
		{"timed out", errors.New("dial tcp 10.0.0.1:443: i/o timed out"), true},
		{"connection refused", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"no such host", errors.New(`dial tcp: lookup gitlab.example.com: no such host`), true},
		{"network unreachable", errors.New("dial tcp: network is unreachable"), true},
		{"no route to host", errors.New("dial tcp: no route to host"), true},
		{"unexpected eof string", errors.New("unexpected EOF"), true},
		{"bad gateway", errors.New("unexpected requesting status code: 502 Bad Gateway"), true},
		{"service unavailable", errors.New("Service Unavailable"), true},
		{"503", errors.New("unexpected client error: unexpected requesting \"https://h/x\" status code: 503"), true},
		{"504", errors.New("unexpected client error: unexpected requesting \"https://h/x\" status code: 504"), true},

		// Auth failures are NOT transient: retrying identical bad credentials
		// fails identically, and the existing auth branch must keep owning them.
		{"auth required sentinel", transport.ErrAuthenticationRequired, false},
		{"auth failed sentinel", transport.ErrAuthorizationFailed, false},
		{"unauthorized string", errors.New("server said: Unauthorized"), false},
		{"authentication string", errors.New("authentication required: bad token"), false},

		// Genuinely local / structural failures must stay fatal so real
		// corruption is still repaired by a reclone.
		{"repo not found", transport.ErrRepositoryNotFound, false},
		{"object not found", errors.New("object not found"), false},
		{"corrupt packfile", errors.New("packfile: invalid checksum"), false},
		{"reference not found", errors.New("reference not found"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTransientNetworkError(tc.err), "err=%v", tc.err)
		})
	}

	// Cross-check the two classifiers are disjoint on the auth sentinels.
	assert.True(t, isAuthError(transport.ErrAuthorizationFailed))
	assert.False(t, isTransientNetworkError(transport.ErrAuthorizationFailed))
	assert.False(t, isAuthError(context.DeadlineExceeded))
	assert.True(t, isTransientNetworkError(context.DeadlineExceeded))
}

// TestDoPull_TransientErrorKeepsExistingClone is the primary regression test
// for the reported data loss: a transient network failure during pull must NOT
// be classified as corruption and must NOT re-clone. The existing clone (its
// files AND its origin remote) has to survive untouched.
func TestDoPull_TransientErrorKeepsExistingClone(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")
	dest := cloneLocal(t, upstream)

	dead := deadServerURL(t)
	setOrigin(t, dest, dead)

	before := snapshotTree(t, dest)

	err := doPull(context.Background(), RepoOpts{URL: dead, Branch: "master", DestDir: dest})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "reclone", "transient failure must not attempt a reclone")

	// Contents preserved.
	assert.FileExists(t, filepath.Join(dest, "wanted.txt"))
	assert.Equal(t, before, snapshotTree(t, dest), "transient pull failure must leave the clone byte-for-byte intact")

	// Origin preserved (a reclone would have rewritten .git/config).
	got, oErr := OriginURL(dest)
	require.NoError(t, oErr)
	assert.Equal(t, dead, got)
}

// TestRecloneForRepair_RefusesOnOriginMismatch pins the core safety property:
// a REPAIR reclone whose configured URL points at a different repository than
// the on-disk origin must refuse outright, name both URLs, and leave the
// directory byte-for-byte untouched. This is what would have prevented the
// gitlab docker-compose-kit clone being replaced by launcher-workspace.
func TestRecloneForRepair_RefusesOnOriginMismatch(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")
	other := newLocalUpstreamNamed(t, "intruder")
	dest := cloneLocal(t, upstream)

	before := snapshotTree(t, dest)

	err := recloneForRepair(context.Background(),
		RepoOpts{URL: other, Branch: "master", DestDir: dest},
		errors.New("pull: TLS handshake timeout"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to re-clone")
	assert.Contains(t, err.Error(), upstream, "error must name the on-disk origin URL")
	assert.Contains(t, err.Error(), other, "error must name the configured URL")

	assert.Equal(t, before, snapshotTree(t, dest), "refused repair must not touch the directory")
	assert.FileExists(t, filepath.Join(dest, "wanted.txt"))
	assert.NoFileExists(t, filepath.Join(dest, "intruder.txt"))
	assert.NoDirExists(t, dest+".tmp", "refused repair must not leave a temp clone behind")
}

// TestRecloneForRepair_ProceedsWhenOriginMatches confirms the guard does not
// break legitimate repair: same repo (modulo cosmetic ".git"/trailing-slash
// differences) => the clone is rebuilt and local debris is discarded.
func TestRecloneForRepair_ProceedsWhenOriginMatches(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")
	dest := cloneLocal(t, upstream)

	// Local debris that only a real reclone removes.
	require.NoError(t, os.WriteFile(filepath.Join(dest, "debris.txt"), []byte("x"), 0o600))

	// Cosmetically different spelling of the same URL must still match.
	err := recloneForRepair(context.Background(),
		RepoOpts{URL: upstream + "/", Branch: "master", DestDir: dest},
		errors.New("hard reset: corrupt index"))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dest, "wanted.txt"))
	assert.NoFileExists(t, filepath.Join(dest, "debris.txt"), "repair reclone should have rebuilt the tree")
	assert.NoDirExists(t, dest+".tmp")
}

// TestRecloneForRepair_ProceedsWhenOriginUnreadable covers genuine corruption:
// if .git cannot be opened at all there is no origin to compare against, and
// refusing would leave the daemon permanently stuck on a broken directory.
func TestRecloneForRepair_ProceedsWhenOriginUnreadable(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")

	dest := filepath.Join(t.TempDir(), "broken")
	require.NoError(t, os.MkdirAll(filepath.Join(dest, ".git"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, ".git", "config"), []byte("garbage"), 0o600))

	err := recloneForRepair(context.Background(),
		RepoOpts{URL: upstream, Branch: "master", DestDir: dest},
		errors.New("open repo: broken"))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest, "wanted.txt"))
}

// TestReclone_RepointStillReplacesContents guards the OTHER intent from
// regressing: when the configured URL deliberately changed, replacing the
// directory is correct and the origin check must NOT apply.
func TestReclone_RepointStillReplacesContents(t *testing.T) {
	oldUpstream := newLocalUpstreamNamed(t, "oldrepo")
	newUpstream := newLocalUpstreamNamed(t, "newrepo")
	dest := cloneLocal(t, oldUpstream)

	err := reclone(context.Background(),
		RepoOpts{URL: newUpstream, Branch: "master", DestDir: dest},
		errors.New("config changed"))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dest, "newrepo.txt"))
	assert.NoFileExists(t, filepath.Join(dest, "oldrepo.txt"))
}

// TestCloneOrPullInner_RepointReplacesContents exercises the same re-point
// intent end-to-end through cloneOrPullInner + the meta sidecar, which is how
// it actually fires in production.
func TestCloneOrPullInner_RepointReplacesContents(t *testing.T) {
	resetGitState(t)
	defer resetGitState(t)

	oldUpstream := newLocalUpstreamNamed(t, "oldrepo")
	newUpstream := newLocalUpstreamNamed(t, "newrepo")
	dest := cloneLocal(t, oldUpstream)

	// Sidecar records the OLD url => repoConfigChanged must see a change.
	writeMeta(t, dest, repoMeta{URL: oldUpstream, Branch: "master"})

	err := cloneOrPullInner(context.Background(),
		RepoOpts{URL: newUpstream, Branch: "master", DestDir: dest})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dest, "newrepo.txt"))
	assert.NoFileExists(t, filepath.Join(dest, "oldrepo.txt"))
}

func writeMeta(t *testing.T, destDir string, meta repoMeta) {
	t.Helper()
	data, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(repoMetaPath(destDir), data, 0o600))
}

// TestRepoConfigChanged_NoMetaFallsBackToOrigin covers D3: a clone made by the
// Kotlin 1.x launcher has no sidecar, so URL drift used to be invisible.
func TestRepoConfigChanged_NoMetaFallsBackToOrigin(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")
	other := newLocalUpstreamNamed(t, "intruder")

	t.Run("differing origin reports changed", func(t *testing.T) {
		dest := cloneLocal(t, upstream)
		require.NoFileExists(t, repoMetaPath(dest))
		assert.True(t, repoConfigChanged(RepoOpts{URL: other, Branch: "master", DestDir: dest}))
	})

	t.Run("matching origin reports unchanged", func(t *testing.T) {
		dest := cloneLocal(t, upstream)
		require.NoFileExists(t, repoMetaPath(dest))
		assert.False(t, repoConfigChanged(RepoOpts{URL: upstream, Branch: "master", DestDir: dest}))
	})

	t.Run("cosmetic url differences do not count as changed", func(t *testing.T) {
		dest := cloneLocal(t, upstream)
		assert.False(t, repoConfigChanged(RepoOpts{URL: upstream + "/", Branch: "master", DestDir: dest}))
	})

	t.Run("unreadable origin stays conservative", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "fake")
		require.NoError(t, os.MkdirAll(filepath.Join(dest, ".git"), 0o750))
		assert.False(t, repoConfigChanged(RepoOpts{URL: other, Branch: "master", DestDir: dest}))
	})

	t.Run("branch-only drift is not detectable without a sidecar", func(t *testing.T) {
		// Documented limitation: a shallow single-branch clone does not
		// reliably record the branch it tracks, so the fallback compares URLs
		// only. Pinned so the limitation is intentional, not accidental.
		dest := cloneLocal(t, upstream)
		assert.False(t, repoConfigChanged(RepoOpts{URL: upstream, Branch: "some-other-branch", DestDir: dest}))
	})

	t.Run("sidecar present keeps meta-based behavior", func(t *testing.T) {
		dest := cloneLocal(t, upstream)
		writeMeta(t, dest, repoMeta{URL: upstream, Branch: "master"})
		assert.False(t, repoConfigChanged(RepoOpts{URL: upstream, Branch: "master", DestDir: dest}))
		assert.True(t, repoConfigChanged(RepoOpts{URL: upstream, Branch: "develop", DestDir: dest}))
		assert.True(t, repoConfigChanged(RepoOpts{URL: other, Branch: "master", DestDir: dest}))
	})
}

// TestNormalizeGitURL pins the comparison rules used by both the repair guard
// and the sidecar-less repoConfigChanged fallback.
func TestNormalizeGitURL(t *testing.T) {
	same := [][2]string{
		{"https://gitlab.citeck.ru/infra/kit.git", "https://gitlab.citeck.ru/infra/kit"},
		{"https://gitlab.citeck.ru/infra/kit.git", "https://gitlab.citeck.ru/infra/kit.git/"},
		{"https://GitLab.Citeck.RU/infra/kit.git", "https://gitlab.citeck.ru/infra/kit.git"},
		{"git@github.com:Citeck/launcher-workspace.git", "git@GitHub.com:Citeck/launcher-workspace"},
		{"/tmp/x/upstream", "/tmp/x/upstream/"},
	}
	for _, p := range same {
		assert.True(t, SameRepoURL(p[0], p[1]), "%q should equal %q", p[0], p[1])
	}

	differ := [][2]string{
		{"https://gitlab.citeck.ru/infrastructure/docker-compose-kit.git", "https://github.com/Citeck/launcher-workspace.git"},
		{"https://github.com/Citeck/a.git", "https://github.com/Citeck/b.git"},
		// Path case IS significant on git hosts; only the host is folded.
		{"https://github.com/Citeck/A.git", "https://github.com/citeck/a.git"},
		{"", "https://github.com/Citeck/a.git"},
	}
	for _, p := range differ {
		assert.False(t, SameRepoURL(p[0], p[1]), "%q should differ from %q", p[0], p[1])
	}
}

// TestOriginURL covers the helper's success and failure modes.
func TestOriginURL(t *testing.T) {
	upstream := newLocalUpstreamNamed(t, "wanted")
	dest := cloneLocal(t, upstream)

	got, err := OriginURL(dest)
	require.NoError(t, err)
	assert.Equal(t, upstream, got)

	_, err = OriginURL(filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err, "missing repo must error")

	// Repo without an origin remote.
	noOrigin := filepath.Join(t.TempDir(), "noorigin")
	_, err = gogit.PlainInit(noOrigin, false)
	require.NoError(t, err)
	_, err = OriginURL(noOrigin)
	assert.Error(t, err, "missing origin remote must error")
}
