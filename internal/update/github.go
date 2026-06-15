package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"
)

// errNotFound wraps a raw fetch that returned 404 so callers can distinguish a
// genuinely-absent file (e.g. a tag predating the changelog feature) from a
// transport/other-status error and degrade gracefully instead of surfacing it.
var errNotFound = errors.New("not found")

const (
	defaultRepo       = "Citeck/citeck-launcher"
	defaultGitHubBase = "https://github.com"
	defaultRawBase    = "https://raw.githubusercontent.com"
	httpTimeout       = 30 * time.Second
	// maxDownloadBytes caps a payload download (the daemon is ~24 MB); a hostile
	// redirect cannot fill the disk before the tar extraction cap applies.
	maxDownloadBytes = 500 << 20 // 500 MiB
)

// client wraps GitHub access. Bases are overridable in tests; production uses the
// defaults. It never follows redirects on the latest-resolution client.
type client struct {
	http       *http.Client
	githubBase string
	rawBase    string
	repo       string
}

// noRedirect builds an HTTP client that does NOT follow redirects (so we can read
// the Location header from /releases/latest) — mirrors install.sh's approach.
func noRedirect() *http.Client {
	return &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// resolveLatest returns the tag (e.g. "v2.6.0") that /releases/latest redirects
// to. Uses no-redirect so the 302 Location is readable.
func (c *client) resolveLatest(ctx context.Context) (string, error) {
	reqURL := fmt.Sprintf("%s/%s/releases/latest", c.githubBase, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build latest request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve latest: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("resolve latest: no redirect (status %d)", resp.StatusCode)
	}
	// Parse the Location and take the last path segment as the tag — path.Base
	// strips any query/fragment and trailing slash so a tag like "v2.6.0" is
	// extracted cleanly even from an absolute redirect URL.
	parsed, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("resolve latest: malformed Location %q: %w", loc, err)
	}
	tag := path.Base(parsed.Path)
	if tag == "" || tag == "." || tag == "/" {
		return "", fmt.Errorf("resolve latest: empty tag in %q", loc)
	}
	return tag, nil
}

// fetchRaw GETs a repo file at ref from raw.githubusercontent.com.
func (c *client) fetchRaw(ctx context.Context, ref, repoPath string) ([]byte, error) {
	rawURL := fmt.Sprintf("%s/%s/%s/%s", c.rawBase, c.repo, ref, repoPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build raw request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", repoPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("fetch %s: %w", repoPath, errNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: status %d", repoPath, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap for changelog files
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", repoPath, err)
	}
	return body, nil
}

// downloadFile streams srcURL to dst atomically (temp + rename).
func (c *client) downloadFile(ctx context.Context, srcURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", srcURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// errNotFound lets callers distinguish a genuinely-absent asset (e.g. a
		// release published without its ".sig" sidecar) from transport errors.
		return fmt.Errorf("download %s: %w", srcURL, errNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: status %d", srcURL, resp.StatusCode)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // payload dir
		return fmt.Errorf("mkdir for download: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Cap the download well above the ~24 MB daemon so a hostile or compromised
	// redirect cannot fill the disk before the extraction cap would apply.
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close download: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename download: %w", err)
	}
	return nil
}

// assetURL builds the release-asset download URL for a given tag + filename.
func (c *client) assetURL(tag, name string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", c.githubBase, c.repo, tag, name)
}
