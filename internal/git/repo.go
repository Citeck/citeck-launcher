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
// window: a "Skip" decision suppresses pulls against the same host for one
// hour. Surfaced as the default of the daemon's git skip-pull endpoint so
// the frontend doesn't have to hard-code the value.
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
//
// Two-level cache: this in-memory map is the hot path; a persistent backing
// store can be plugged in via SetSyncStateStore so throttling survives restart
// (Kotlin parity with `git-repo!instances`). On every recordSync we write
// through to the backing store; on first access for an unknown DestDir we
// fault in from the backing store under the same mutex.
var (
	lastSyncMu    sync.Mutex
	lastSyncTimes = make(map[string]time.Time)
	cloneFlight   singleflight.Group // 8b-12: dedup concurrent ops on same dir
)

// SyncStateEntry mirrors storage.GitRepoState without dragging the storage
// package into this one — keeps the dependency direction acyclic.
type SyncStateEntry struct {
	Path           string
	LastSyncMs     int64
	LastCommitHash string
}

// SyncStateStore is the persistence hook used to make repo throttling
// restart-survivable. Implementations are expected to be safe for concurrent
// use; this package serializes all reads/writes under lastSyncMu anyway, but
// that's an implementation detail, not a guarantee.
type SyncStateStore interface {
	GetGitRepoState(path string) (*SyncStateEntry, error)
	SetGitRepoState(entry SyncStateEntry) error
}

var (
	syncStoreMu     sync.RWMutex
	syncStore       SyncStateStore
	syncStoreRoot   string
	syncStoreLoaded = make(map[string]struct{})
)

// SetSyncStateStore wires a persistent backing store and the launcher home
// directory (used to derive Kotlin-compatible relative paths). Pass (nil, "")
// to detach, e.g. on shutdown. The map of known sync times is NOT cleared so
// in-flight pulls keep their cached values for the rest of this process's
// lifetime; only the write-through is suspended.
func SetSyncStateStore(store SyncStateStore, homeDir string) {
	syncStoreMu.Lock()
	defer syncStoreMu.Unlock()
	syncStore = store
	syncStoreRoot = homeDir
	syncStoreLoaded = make(map[string]struct{})
}

// relativeRepoPath returns a forward-slash relative path from the launcher
// home to destDir — the same key Kotlin's GitRepoService uses in
// `git-repo!instances`. Returns destDir verbatim when the home dir is unset
// or destDir doesn't sit under it (defensive: keeps callers working with
// absolute paths even outside the standard tree).
func relativeRepoPath(destDir string) string {
	syncStoreMu.RLock()
	root := syncStoreRoot
	syncStoreMu.RUnlock()
	if root == "" {
		return destDir
	}
	rel, err := filepath.Rel(root, destDir)
	if err != nil {
		return destDir
	}
	return filepath.ToSlash(rel)
}

// loadPersistedSyncLocked faults in the persisted lastSync timestamp the first
// time a repo path is queried, so a fresh process honors the throttle window
// set by its predecessor. Must be called with lastSyncMu held.
func loadPersistedSyncLocked(destDir string) {
	syncStoreMu.RLock()
	store := syncStore
	syncStoreMu.RUnlock()
	if store == nil {
		return
	}
	if _, ok := syncStoreLoaded[destDir]; ok {
		return
	}
	syncStoreLoaded[destDir] = struct{}{}
	entry, err := store.GetGitRepoState(relativeRepoPath(destDir))
	if err != nil || entry == nil {
		return
	}
	if _, already := lastSyncTimes[destDir]; already {
		return
	}
	lastSyncTimes[destDir] = time.UnixMilli(entry.LastSyncMs)
}

// Host-level pull suppression: when the user clicks Skip in GitPullErrorDialog,
// the decision is remembered for an hour so subsequent pulls against the same
// host (e.g. all bundle repos hosted on a temporarily-broken GitLab instance)
// don't re-prompt. Kotlin parity: `skipPullForRepoDecisionAt` map keyed by host.
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
		if _, after, ok := strings.Cut(repoURL, "@"); ok {
			if host, _, ok2 := strings.Cut(after, ":"); ok2 {
				return strings.ToLower(host)
			}
			return strings.ToLower(after)
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

// pullHardDeadlineDefault is the wall-clock backstop used when the caller's
// context carries no deadline; pullHardDeadlineGrace is added on top of a
// context deadline. A var (not const) so tests can shorten it.
const pullHardDeadlineDefault = 3 * time.Minute

var pullHardDeadlineGrace = 30 * time.Second

// cloneOrPullRunner performs the actual (deduped) clone/pull. Indirected
// through a var so the hard-deadline backstop in CloneOrPullWithAuth can be
// unit-tested with a blocking stand-in.
var cloneOrPullRunner = func(ctx context.Context, opts RepoOpts) error {
	// 8b-12: dedup concurrent clone/pull on same directory
	_, err, _ := cloneFlight.Do(opts.DestDir, func() (any, error) {
		return nil, cloneOrPullInner(ctx, opts)
	})
	return err //nolint:wrapcheck // wrapped by CloneOrPullWithAuth's done-branch
}

// CloneOrPullWithAuth clones or pulls with optional token authentication and sync throttling.
// If the repo URL or branch has changed since the last clone, the repo is re-cloned from scratch.
// Accepts context for timeout/cancellation support.
func CloneOrPullWithAuth(ctx context.Context, opts RepoOpts) error {
	// Hard wall-clock backstop: go-git's clone/pull do not reliably honor ctx
	// cancellation — a stalled TLS read or a corrupt-pack loop (e.g. after an
	// ENOSPC episode) can block well past the deadline. Without this, a hung pull
	// holds the caller and any lock it owns (the daemon's reloadMu during a
	// reload) indefinitely, wedging the daemon. Run in a goroutine and return on
	// the deadline regardless; the caller falls back to the on-disk repo (resolve
	// uses the cached bundle on error). The abandoned goroutine finishes or dies
	// on its own; cloneFlight dedups so a later call shares its result.
	hard := pullHardDeadlineDefault
	if dl, ok := ctx.Deadline(); ok {
		hard = time.Until(dl) + pullHardDeadlineGrace
	}
	if hard < pullHardDeadlineGrace {
		hard = pullHardDeadlineGrace
	}
	// Capture the runner before launching the goroutine so an abandoned (timed
	// out) goroutine never reads the package var concurrently with a test swap.
	runner := cloneOrPullRunner
	done := make(chan error, 1)
	go func() { done <- runner(ctx, opts) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("clone or pull %s: %w", opts.DestDir, err)
		}
		return nil
	case <-time.After(hard):
		slog.Warn("git clone/pull exceeded hard deadline; abandoning and using on-disk repo",
			"dir", opts.DestDir, "deadline", hard.String())
		return fmt.Errorf("clone or pull %s: hard deadline %s exceeded", opts.DestDir, hard)
	}
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

	// Host-level pull suppression (Kotlin parity). When
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
		loadPersistedSyncLocked(opts.DestDir)
		lastSync := lastSyncTimes[opts.DestDir]
		lastSyncMu.Unlock()
		if !lastSync.IsZero() && time.Since(lastSync) < opts.PullPeriod {
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
	now := time.Now()
	lastSyncMu.Lock()
	lastSyncTimes[opts.DestDir] = now
	syncStoreLoaded[opts.DestDir] = struct{}{}
	lastSyncMu.Unlock()
	saveRepoMeta(opts)

	syncStoreMu.RLock()
	store := syncStore
	syncStoreMu.RUnlock()
	if store == nil {
		return
	}
	commitHash, _ := readHeadHash(opts.DestDir)
	entry := SyncStateEntry{
		Path:           relativeRepoPath(opts.DestDir),
		LastSyncMs:     now.UnixMilli(),
		LastCommitHash: commitHash,
	}
	if err := store.SetGitRepoState(entry); err != nil {
		slog.Warn("Failed to persist git repo sync state", "path", entry.Path, "err", err)
	}
}

// readHeadHash returns the HEAD commit hash of a working repo. Best-effort:
// failures (broken HEAD, missing .git) collapse to "" so a sync record still
// gets written with the timestamp — the hash is a Kotlin-parity convenience,
// not load-bearing for the throttle.
func readHeadHash(destDir string) (string, error) {
	repo, err := gogit.PlainOpen(destDir)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("head: %w", err)
	}
	return head.Hash().String(), nil
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
		// 8b-13: auth errors (401/403) should not trigger reclone — a fresh
		// clone would hit the same credential problem and just waste bandwidth.
		if isAuthError(err) {
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

// IsAuthError reports whether an error is caused by a git authentication /
// authorization failure (401/403). Exported so callers outside this package
// (bundle resolver, daemon workspace handlers) classify workspace-repo sync
// failures consistently with the pull/reclone logic here — the same
// "authentication required" / "unauthorized" wording the Web UI's
// isGitPullError heuristic matches on.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) {
		return true
	}
	// Fallback: some transports wrap auth errors without using sentinel values
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "authentication")
}

// isAuthError is the internal alias kept so the pull/reclone call sites read
// unchanged; new code should use the exported IsAuthError.
func isAuthError(err error) bool { return IsAuthError(err) }
