package git

// Reclone safety: deciding when replacing a clone's contents is legitimate.
//
// Split out of repo.go, which crossed the project's file-size guideline. The
// unit here is one policy — "may we overwrite this directory with a fresh
// clone?" — plus the URL/origin comparison and error classification that policy
// depends on. repo.go keeps clone/pull orchestration and the sync bookkeeping.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// OriginURL reads the first URL of the clone's "origin" remote — the repository
// the on-disk working copy actually came from (and, in doPull, the one it pulls
// from: RepoOpts.URL is never written back onto the remote).
func OriginURL(destDir string) (string, error) {
	repo, err := gogit.PlainOpen(destDir)
	if err != nil {
		return "", fmt.Errorf("open repo %s: %w", destDir, err)
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("read origin remote of %s: %w", destDir, err)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", fmt.Errorf("origin remote of %s has no URL", destDir)
	}
	return urls[0], nil
}

// SameRepoURL reports whether two git URLs designate the same repository,
// ignoring cosmetic spelling differences (trailing slash, ".git" suffix, host
// case). Used to decide whether replacing a clone's contents is safe, so it is
// deliberately conservative: anything beyond those cosmetic differences counts
// as a DIFFERENT repository. Path case is significant (git hosts are
// case-sensitive in the path); only scheme and host are folded.
func SameRepoURL(a, b string) bool {
	return normalizeGitURL(a) == normalizeGitURL(b)
}

func normalizeGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	if u, err := url.Parse(s); err == nil && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		return u.String()
	}
	// SCP-like form (git@Host:org/repo) and bare filesystem paths: url.Parse
	// gives no host, so lowercase just the host span by hand.
	if at := strings.Index(s, "@"); at >= 0 {
		if colon := strings.Index(s[at:], ":"); colon > 0 {
			hostStart, hostEnd := at+1, at+colon
			return s[:hostStart] + strings.ToLower(s[hostStart:hostEnd]) + s[hostEnd:]
		}
	}
	return s
}

// Re-cloning has two DISTINCT intents and they must not share a policy:
//
//   - RE-POINT (reclone): the configured URL/branch deliberately changed, so
//     replacing the directory contents with a different repository is the whole
//     point. No origin check.
//   - REPAIR (recloneForRepair): something went wrong with an existing clone and
//     we want the SAME repository back. Substituting a different repository here
//     is data loss, so the on-disk origin is verified first.
//
// Both share recloneReplace for the atomic temp-clone-and-swap mechanics.

// reclone re-clones because the configured URL/branch changed (re-point).
// Replacing the directory contents is the intended outcome.
func reclone(ctx context.Context, opts RepoOpts, cause error) error {
	return recloneReplace(ctx, opts, cause, "config changed (re-point)")
}

// recloneForRepair re-clones to recover an existing clone that failed to open,
// reset or pull. It refuses when the on-disk origin names a different
// repository than opts.URL: that combination means the launcher's repo config
// has drifted (e.g. workspace settings lost or defaulted), and cloning opts.URL
// over the top would replace the user's repository with an unrelated one — the
// exact failure being fixed here. Refusing leaves the directory untouched and
// returns an error naming BOTH URLs so the cause is diagnosable from the log.
//
// If the origin cannot be read at all, the .git directory really is broken;
// that IS genuine corruption, so the repair proceeds.
func recloneForRepair(ctx context.Context, opts RepoOpts, cause error) error {
	origin, err := OriginURL(opts.DestDir)
	switch {
	case err != nil:
		slog.Warn("Cannot read origin remote; treating as genuine corruption and re-cloning",
			"dir", opts.DestDir, "err", err)
	case !SameRepoURL(origin, opts.URL):
		slog.Error("Refusing to re-clone: on-disk origin does not match the configured URL",
			"dir", opts.DestDir, "origin", origin, "configured", opts.URL, "cause", cause)
		return fmt.Errorf(
			"refusing to re-clone %s: on-disk origin %q does not match configured URL %q "+
				"(workspace repo settings may be stale or lost); triggering failure: %w",
			opts.DestDir, origin, opts.URL, cause)
	}
	return recloneReplace(ctx, opts, cause, "repairing existing clone")
}

// recloneReplace clones to a temp directory and swaps on success. If clone fails, the old
// directory is kept intact so the daemon can continue with stale data.
func recloneReplace(ctx context.Context, opts RepoOpts, cause error, intent string) error {
	slog.Warn("Re-cloning repository; existing directory contents will be replaced",
		"dir", opts.DestDir, "url", opts.URL, "intent", intent, "cause", cause)

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

// isAuthError reports whether an error is caused by a git authentication /
// authorization failure (401/403). Used by the pull/reclone retry logic to
// stop retrying on bad credentials. Workspace-repo sync failures propagate
// their go-git wording verbatim (%w) instead — the bundle resolver and daemon
// workspace handlers keep the "authentication required" / "unauthorized" text
// so the Web UI's isGitPullError heuristic can match on it.
func isAuthError(err error) bool {
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

// transientErrSubstrings is the string-matching fallback for
// isTransientNetworkError. go-git (and the HTTP transport underneath it)
// flattens most transport failures into opaque error strings with no wrapped
// sentinel to match on, so substrings are the only signal left.
//
// The bare status codes are intentionally coarse. A false positive here only
// means "don't re-clone, keep the existing clone and report the error" — the
// fail-safe direction. A false negative destroys a working clone.
var transientErrSubstrings = []string{
	"timeout",
	"timed out",
	"connection refused",
	"connection reset",
	"no such host",
	"network is unreachable",
	"no route to host",
	"tls handshake",
	"unexpected eof",
	"bad gateway",
	"service unavailable",
	"502",
	"503",
	"504",
}

// isTransientNetworkError reports whether an error is a momentary network /
// server-side failure rather than evidence that the local clone is damaged.
//
// This is the guard that keeps doPull from treating a TLS handshake timeout as
// "repo corrupted": such an error tells us nothing about the on-disk repo, so
// re-cloning over it is both pointless and (given URL drift) destructive.
// Sibling of isAuthError; the two are mutually exclusive by construction.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// Auth failures are decisively NOT transient — retrying the same bad
	// credentials fails identically. Checked first so an auth response whose
	// body happens to mention e.g. a timeout can't be misfiled here; the
	// caller's auth branch owns them.
	if isAuthError(err) {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	errStr := strings.ToLower(err.Error())
	for _, s := range transientErrSubstrings {
		if strings.Contains(errStr, s) {
			return true
		}
	}
	return false
}
