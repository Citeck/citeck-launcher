package git

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"golang.org/x/sync/singleflight"
)

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
	return err
}

func cloneOrPullInner(ctx context.Context, opts RepoOpts) error {
	gitDir := filepath.Join(opts.DestDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Check if URL or branch changed — re-clone if so
		if repoConfigChanged(opts) {
			slog.Info("Repo config changed, re-cloning", "dir", opts.DestDir, "url", opts.URL, "branch", opts.Branch)
			err := reclone(ctx, opts, fmt.Errorf("config changed: url=%s branch=%s", opts.URL, opts.Branch))
			if err == nil {
				recordSync(opts)
			}
			return err
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
	err := doClone(ctx, opts)
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
	os.WriteFile(repoMetaPath(opts.DestDir), data, 0o644)
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
	return &http.BasicAuth{
		Username: "x-token-auth",
		Password: token,
	}
}

func doClone(ctx context.Context, opts RepoOpts) error {
	slog.Info("Cloning repository", "url", opts.URL, "branch", opts.Branch, "dir", opts.DestDir)
	start := time.Now()

	if err := os.MkdirAll(filepath.Dir(opts.DestDir), 0o755); err != nil {
		return err
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

	if err := wt.PullContext(ctx, pullOpts); err != nil && err != gogit.NoErrAlreadyUpToDate {
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
	// If reclone requires auth we don't have, skip the attempt entirely —
	// the stale repo is usable and the reclone would fail anyway.
	if opts.Token == "" && requiresAuth(opts.URL) {
		slog.Info("Skipping reclone (no auth token, using stale repo)", "dir", opts.DestDir, "cause", cause)
		return fmt.Errorf("repo needs repair but auth unavailable for %s: %w", opts.URL, cause)
	}

	slog.Warn("Repo corrupted, re-cloning", "dir", opts.DestDir, "cause", cause)

	tmpDir := opts.DestDir + ".tmp"
	// Clean up any leftover temp dir from a previous failed attempt
	os.RemoveAll(tmpDir)

	tmpOpts := opts
	tmpOpts.DestDir = tmpDir
	if err := doClone(ctx, tmpOpts); err != nil {
		os.RemoveAll(tmpDir)
		if isAuthError(err) {
			slog.Info("Reclone auth failed, keeping stale repo", "dir", opts.DestDir)
		} else {
			slog.Warn("Reclone failed, keeping stale repo", "dir", opts.DestDir, "err", err)
		}
		return fmt.Errorf("reclone %s: %w", opts.URL, err)
	}

	if err := os.RemoveAll(opts.DestDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("remove old repo %s: %w", opts.DestDir, err)
	}
	if err := os.Rename(tmpDir, opts.DestDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("rename %s -> %s: %w", tmpDir, opts.DestDir, err)
	}
	return nil
}

// isAuthError checks if an error is caused by authentication failure.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "authentication") || strings.Contains(errStr, "unauthorized")
}

// requiresAuth checks if the URL likely requires authentication (non-public git hosts).
func requiresAuth(url string) bool {
	lower := strings.ToLower(url)
	// Public hosts that allow anonymous read access are rare; most private
	// GitLab/GitHub repos require auth. Check for common private patterns.
	return strings.Contains(lower, "gitlab.") || strings.Contains(lower, "github.") ||
		strings.Contains(lower, "bitbucket.")
}
