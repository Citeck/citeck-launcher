package git

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
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
)

// CloneOrPull clones a repo if not present, or pulls latest changes.
func CloneOrPull(repoURL, branch, destDir string) error {
	return CloneOrPullWithAuth(RepoOpts{URL: repoURL, Branch: branch, DestDir: destDir})
}

// CloneOrPullWithAuth clones or pulls with optional token authentication and sync throttling.
func CloneOrPullWithAuth(opts RepoOpts) error {
	if _, err := os.Stat(filepath.Join(opts.DestDir, ".git")); err == nil {
		if opts.PullPeriod > 0 {
			lastSyncMu.Lock()
			lastSync := lastSyncTimes[opts.DestDir]
			lastSyncMu.Unlock()
			if time.Since(lastSync) < opts.PullPeriod {
				slog.Debug("Skipping pull (within pull period)", "dir", opts.DestDir)
				return nil
			}
		}
		err := doPull(opts)
		if err == nil {
			recordSync(opts.DestDir)
		}
		return err
	}
	err := doClone(opts)
	if err == nil {
		recordSync(opts.DestDir)
	}
	return err
}

func recordSync(destDir string) {
	lastSyncMu.Lock()
	lastSyncTimes[destDir] = time.Now()
	lastSyncMu.Unlock()
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

func doClone(opts RepoOpts) error {
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

	if _, err := gogit.PlainClone(opts.DestDir, false, cloneOpts); err != nil {
		return fmt.Errorf("git clone %s: %w", opts.URL, err)
	}

	slog.Info("Repository cloned", "dir", opts.DestDir, "elapsed", time.Since(start))
	return nil
}

func doPull(opts RepoOpts) error {
	slog.Info("Pulling repository", "dir", opts.DestDir, "branch", opts.Branch)

	repo, err := gogit.PlainOpen(opts.DestDir)
	if err != nil {
		return fmt.Errorf("open repo %s: %w", opts.DestDir, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree %s: %w", opts.DestDir, err)
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

	if err := wt.Pull(pullOpts); err != nil && err != gogit.NoErrAlreadyUpToDate {
		return fmt.Errorf("git pull in %s: %w", opts.DestDir, err)
	}
	return nil
}
