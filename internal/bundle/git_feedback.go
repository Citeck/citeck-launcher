package bundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	launchergit "github.com/citeck/citeck-launcher/internal/git"
)

// RepoSyncError preserves repository identity and verified cache availability.
type RepoSyncError struct {
	URL            string
	CacheAvailable bool
	LastSyncAt     int64
	Err            error
	SecretID       *string
}

func (e *RepoSyncError) Error() string {
	return fmt.Sprintf("sync repository %s: %s", SafeGitURL(e.URL), launchergit.SafeError(e.Err, e.URL))
}
func (e *RepoSyncError) Unwrap() error { return e.Err }

// SafeGitURL removes embedded credentials and query/fragment data from metadata.
func SafeGitURL(raw string) string { return launchergit.SafeURL(raw) }

// WithTokenIdentifiers attaches non-secret references from the same credential
// snapshot as TokenLookupFunc, so recovery never guesses after a token changed.
func (r *Resolver) WithTokenIdentifiers(lookup func(string) string) *Resolver {
	r.tokenIDLookup = lookup
	return r
}

// WithCachedRepos carries explicit cache choices through one retry chain.
func (r *Resolver) WithCachedRepos(repoURLs []string) *Resolver {
	r.cachedRepoURLs = make(map[string]bool, len(repoURLs))
	for _, raw := range repoURLs {
		if safe := SafeGitURL(raw); safe != "" {
			r.cachedRepoURLs[safe] = true
		}
	}
	return r
}
func (r *Resolver) usesCachedRepo(repoURL string) bool {
	return r.cachedRepoURLs[SafeGitURL(repoURL)]
}
func repoSyncFailure(repoURL, dir string, err error) *RepoSyncError {
	return &RepoSyncError{URL: repoURL, LastSyncAt: launchergit.LastSuccessfulSync(dir), Err: err}
}

// CachedRepoMatches distinguishes a same-repository cache from a clone left
// behind by a failed URL change. Manual copies without .git remain usable.
func CachedRepoMatches(dir, configuredURL string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		return true
	}
	origin, err := launchergit.OriginURL(dir)
	return err == nil && launchergit.SameRepoURL(SafeGitURL(origin), SafeGitURL(configuredURL))
}
