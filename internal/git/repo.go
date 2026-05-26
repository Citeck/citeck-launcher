package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"golang.org/x/sync/singleflight"
)

// DefaultSkipPullDuration is the Kotlin-parity host-level pull suppression
// window (docs/porting/07 §1.9: "Skip … записывает host в
// skipPullForRepoDecisionAt на 1 час"). Surfaced as the default of the
// daemon's git skip-pull endpoint so the frontend doesn't have to hard-code
// the value.
const DefaultSkipPullDuration = time.Hour

// ErrPullSkippedByUser is returned by pull operations when the host has been
// skipped via SkipPullForHost. Surfaces as an info-level outcome rather than
// a failure; callers should treat it like "pull period not elapsed" and
// continue with the existing local clone.
var ErrPullSkippedByUser = errors.New("git pull skipped by user decision")

// RepoOpts configures a git clone/pull operation.
type RepoOpts struct {
	URL     string
	Branch  string
	DestDir string
	// Token for token-based authentication (GitHub PAT, GitLab deploy token, etc.).
	Token string
	// PullPeriod skips pull if last sync was within this duration. Zero means always pull.
	PullPeriod time.Duration
}

// lastSyncTimes tracks when each repo was last synced.
var (
	lastSyncMu    sync.Mutex
	lastSyncTimes = make(map[string]time.Time)
	cloneFlight   singleflight.Group // 8b-12: dedup concurrent ops on same dir
)

// Host-level pull suppression: when the user clicks Skip in GitPullErrorDialog,
// the decision is remembered for an hour so subsequent pulls against the same
// host (e.g. all bundle repos hosted on a temporarily-broken GitLab instance)
// don't re-prompt. Kotlin parity: docs/porting/07 §1.9
// (`skipPullForRepoDecisionAt` map keyed by host).
var (
	skipMu          sync.Mutex
	skipUntilByHost = make(map[string]time.Time)
)

// SkipPullForHost suppresses pull operations against `host` until `time.Now() + d`.
// `host` is the bare hostname (no scheme, no port); use HostFromURL to derive it
// from a repo URL. Setting d <= 0 clears any existing skip for that host.
func SkipPullForHost(host string, d time.Duration) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	skipMu.Lock()
	defer skipMu.Unlock()
	if d <= 0 {
		delete(skipUntilByHost, host)
		return
	}
	skipUntilByHost[host] = time.Now().Add(d)
}

// IsSkipped reports whether `host` is currently within an active skip window.
// Hosts whose skip window has elapsed are evicted lazily on the next check.
func IsSkipped(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	skipMu.Lock()
	defer skipMu.Unlock()
	until, ok := skipUntilByHost[host]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(skipUntilByHost, host)
		return false
	}
	return true
}

// HostFromURL extracts the bare hostname (lowercased, port stripped) from a
// git URL. Supports https/ssh URLs and the `git@host:path` SCP form. Returns
// empty string if no host can be parsed.
func HostFromURL(repoURL string) string {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return ""
	}
	// SCP-like form: git@host:user/repo.git
	if !strings.Contains(repoURL, "://") {
		if at := strings.Index(repoURL, "@"); at >= 0 {
			rest := repoURL[at+1:]
			if colon := strings.Index(rest, ":"); colon >= 0 {
				return strings.ToLower(rest[:colon])
			}
			return strings.ToLower(rest)
		}
		return ""
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// CloneOrPull clones a repo if not present, or pulls latest changes.
func CloneOrPull(repoURL, branch, destDir string) error {
	return CloneOrPullWithAuth(context.Background(), RepoOpts{URL: repoURL, Branch: branch, DestDir: destDir})
}

// CloneOrPullWithAuth clones or pulls with optional token authentication and sync throttling.
// If the repo URL or branch has changed since the last clone, the repo is re-cloned from scratch.
// Accepts context for timeout/cancellation support.
func CloneOrPullWithAuth(ctx context.Context, opts RepoOpts) error {
	// 8b-12: dedup concurrent clone/pull on same directory
	_, err, _ := cloneFlight.Do(opts.DestDir, func() (any, error) {
		return nil, cloneOrPullInner(ctx, opts)
	})
	if err != nil {
		return fmt.Errorf("clone or pull %s: %w", opts.DestDir, err)
	}
	return nil
}

func cloneOrPullInner(ctx context.Context, opts RepoOpts) error {
	gitDir := filepath.Join(opts.DestDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		// No .git directory — fresh clone. The host-skip flag does NOT apply
		// here: skipping a clone would leave the daemon with no usable repo
		// at all (worse than a noisy retry against a flaky host). Kotlin
		// behaves identically — `GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS` lets
		// clone proceed even when Skip is active.
		cloneErr := doClone(ctx, opts)
		if cloneErr == nil {
			recordSync(opts)
		}
		return cloneErr
	}

	// Check if URL or branch changed — re-clone if so
	if repoConfigChanged(opts) {
		slog.Info("Repo config changed, re-cloning", "dir", opts.DestDir, "url", opts.URL, "branch", opts.Branch)
		err := reclone(ctx, opts, fmt.Errorf("config changed: url=%s branch=%s", opts.URL, opts.Branch))
		if err == nil {
			recordSync(opts)
		}
		return err
	}

	// Host-level pull suppression (Kotlin parity: docs/porting/07 §1.9). When
	// the user clicked Skip in GitPullErrorDialog within the last hour, treat
	// the pull as a no-op for any repo on the same host so we don't re-prompt
	// for bundle-repo / workspace-repo siblings that all live on a temporarily-
	// broken GitLab instance. Returning nil (rather than ErrPullSkippedByUser)
	// preserves the existing local clone unchanged — same outcome as throttled
	// pulls.
	if host := HostFromURL(opts.URL); host != "" && IsSkipped(host) {
		slog.Info("Skipping pull (host-level user skip active)", "host", host, "dir", opts.DestDir)
		return nil
	}

	if opts.PullPeriod > 0 {
		lastSyncMu.Lock()
		lastSync := lastSyncTimes[opts.DestDir]
		lastSyncMu.Unlock()
		if time.Since(lastSync) < opts.PullPeriod {
			slog.Debug("Skipping pull (within pull period)", "dir", opts.DestDir)
			return nil
		}
	}
	err := doPull(ctx, opts)
	if err == nil {
		recordSync(opts)
	}
	return err
}

// repoMeta is stored alongside the clone to detect URL/branch changes.
type repoMeta struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
}

func repoMetaPath(destDir string) string {
	return filepath.Join(destDir, ".git", "citeck-repo-meta.json")
}

func repoConfigChanged(opts RepoOpts) bool {
	data, err := os.ReadFile(repoMetaPath(opts.DestDir))
	if err != nil {
		return false // no meta — assume not changed (first run after upgrade)
	}
	var meta repoMeta
	if json.Unmarshal(data, &meta) != nil {
		return false
	}
	return meta.URL != opts.URL || meta.Branch != opts.Branch
}

func saveRepoMeta(opts RepoOpts) {
	meta := repoMeta{URL: opts.URL, Branch: opts.Branch}
	data, _ := json.Marshal(meta)
	_ = os.WriteFile(repoMetaPath(opts.DestDir), data, 0o644) //nolint:gosec // G306: repo meta needs 0o644 for readability
}

func recordSync(opts RepoOpts) {
	lastSyncMu.Lock()
	lastSyncTimes[opts.DestDir] = time.Now()
	lastSyncMu.Unlock()
	saveRepoMeta(opts)
}

func tokenAuth(token string) *http.BasicAuth {
	if token == "" {
		return nil
	}
	// "x-token-auth" is the GitLab convention for token auth over BasicAuth;
	// GitHub accepts any non-empty username with a PAT, so this same username
	// works for both providers and for self-hosted Gitea / Forgejo.
	return &http.BasicAuth{
		Username: "x-token-auth",
		Password: token,
	}
}

func doClone(ctx context.Context, opts RepoOpts) error {
	slog.Info("Cloning repository", "url", opts.URL, "branch", opts.Branch, "dir", opts.DestDir)
	start := time.Now()

	if err := os.MkdirAll(filepath.Dir(opts.DestDir), 0o750); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", opts.DestDir, err)
	}

	cloneOpts := &gogit.CloneOptions{
		URL:           opts.URL,
		ReferenceName: plumbing.NewBranchReferenceName(opts.Branch),
		Depth:         1,
		SingleBranch:  true,
	}
	if auth := tokenAuth(opts.Token); auth != nil {
		cloneOpts.Auth = auth
	}

	if _, err := gogit.PlainCloneContext(ctx, opts.DestDir, false, cloneOpts); err != nil {
		return fmt.Errorf("git clone %s: %w", opts.URL, err)
	}

	slog.Info("Repository cloned", "dir", opts.DestDir, "elapsed", time.Since(start))
	return nil
}

// TestAuth verifies a git token by running ls-remote against the given URL.
// Returns nil if authentication succeeds.
func TestAuth(repoURL, token string) error {
	remote := gogit.NewRemote(nil, &gogitconfig.RemoteConfig{
		Name: "test",
		URLs: []string{repoURL},
	})

	listOpts := &gogit.ListOptions{}
	if auth := tokenAuth(token); auth != nil {
		listOpts.Auth = auth
	}

	_, err := remote.List(listOpts)
	if err != nil {
		return fmt.Errorf("ls-remote %s: %w", repoURL, err)
	}
	return nil
}

func doPull(ctx context.Context, opts RepoOpts) error {
	slog.Info("Pulling repository", "dir", opts.DestDir, "branch", opts.Branch)

	repo, err := gogit.PlainOpen(opts.DestDir)
	if err != nil {
		return reclone(ctx, opts, fmt.Errorf("open repo: %w", err))
	}

	wt, err := repo.Worktree()
	if err != nil {
		return reclone(ctx, opts, fmt.Errorf("worktree: %w", err))
	}

	// Hard-reset working tree to ensure clean state before pulling.
	// Matches Kotlin's ResetType.HARD behavior — discards any local modifications.
	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", opts.Branch), true)
	if err == nil {
		if err := wt.Reset(&gogit.ResetOptions{Commit: remoteRef.Hash(), Mode: gogit.HardReset}); err != nil {
			// Corrupted packfile index or similar — re-clone from scratch
			return reclone(ctx, opts, fmt.Errorf("hard reset: %w", err))
		}
	}

	pullOpts := &gogit.PullOptions{
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(opts.Branch),
		SingleBranch:  true,
		Depth:         1,
	}
	if auth := tokenAuth(opts.Token); auth != nil {
		pullOpts.Auth = auth
	}

	if err := wt.PullContext(ctx, pullOpts); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		// 8b-13: auth errors should not trigger reclone
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "authentication") || strings.Contains(errStr, "unauthorized") {
			return fmt.Errorf("git auth failed for %s: %w", opts.URL, err)
		}
		return reclone(ctx, opts, fmt.Errorf("pull: %w", err))
	}
	return nil
}

// reclone clones to a temp directory and swaps on success. If clone fails, the old
// directory is kept intact so the daemon can continue with stale data.
func reclone(ctx context.Context, opts RepoOpts, cause error) error {
	slog.Warn("Repo corrupted, re-cloning", "dir", opts.DestDir, "cause", cause)

	tmpDir := opts.DestDir + ".tmp"
	// Clean up any leftover temp dir from a previous failed attempt
	_ = os.RemoveAll(tmpDir)

	tmpOpts := opts
	tmpOpts.DestDir = tmpDir
	if err := doClone(ctx, tmpOpts); err != nil {
		_ = os.RemoveAll(tmpDir)
		if isAuthError(err) {
			slog.Info("Reclone auth failed, keeping stale repo", "dir", opts.DestDir)
		} else {
			slog.Warn("Reclone failed, keeping stale repo", "dir", opts.DestDir, "err", err)
		}
		return fmt.Errorf("reclone %s: %w", opts.URL, err)
	}

	if err := os.RemoveAll(opts.DestDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("remove old repo %s: %w", opts.DestDir, err)
	}
	if err := os.Rename(tmpDir, opts.DestDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("rename %s -> %s: %w", tmpDir, opts.DestDir, err)
	}
	return nil
}

// isAuthError checks if an error is caused by authentication failure.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) {
		return true
	}
	// Fallback: some transports wrap auth errors without using sentinel values
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "unauthorized")
}
