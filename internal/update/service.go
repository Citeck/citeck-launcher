package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// periodicInterval is how often the service re-checks `latest` while the app runs.
const periodicInterval = 4 * time.Hour

// Status is the UI-facing snapshot of the updater.
type Status struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	Available      bool   `json:"available"`
	LastCheckAt    string `json:"lastCheckAt,omitempty"`
	Error          string `json:"error,omitempty"`      // last check error (offline etc.)
	ApplyError     string `json:"applyError,omitempty"` // last failed apply, rolled back
	Applying       bool   `json:"applying"`
}

// Service orchestrates discovery, changelog, and staging. It holds no Wails or
// daemon imports. The wrapper performs the swap; the daemon HTTP handlers call
// this service.
type Service struct {
	current    string
	updatesDir string

	repo           string
	githubBase     string
	rawBase        string
	http           *http.Client // raw + download (follows redirects)
	noRedirectHTTP *http.Client // latest resolution (no redirect)

	applying atomic.Bool

	mu        sync.Mutex
	latest    Latest
	lastCheck time.Time
	lastErr   string
}

// Option overrides Service defaults. Production callers use NewService with no
// options; tests / integration harnesses point the Service at a mock GitHub via
// WithBaseURLs + WithRepo (see internal/update/updatetest).
type Option func(*Service)

// WithBaseURLs overrides the GitHub and raw-content base URLs (e.g. an httptest
// mock server). Both default to the real github.com / raw.githubusercontent.com.
func WithBaseURLs(githubBase, rawBase string) Option {
	return func(s *Service) {
		s.githubBase = githubBase
		s.rawBase = rawBase
	}
}

// WithRepo overrides the "owner/name" repo slug (default Citeck/citeck-launcher).
func WithRepo(repo string) Option {
	return func(s *Service) { s.repo = repo }
}

// NewService builds a Service for the given running version and updates dir.
func NewService(current, updatesDir string, opts ...Option) *Service {
	s := &Service{
		current:        current,
		updatesDir:     updatesDir,
		repo:           defaultRepo,
		githubBase:     defaultGitHubBase,
		rawBase:        defaultRawBase,
		http:           &http.Client{Timeout: httpTimeout},
		noRedirectHTTP: noRedirect(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) latestClient() *client {
	return &client{http: s.noRedirectHTTP, githubBase: s.githubBase, rawBase: s.rawBase, repo: s.repo}
}

func (s *Service) dataClient() *client {
	return &client{http: s.http, githubBase: s.githubBase, rawBase: s.rawBase, repo: s.repo}
}

// CheckLatest resolves the newest release and caches it. Errors are cached for
// Status and returned to the caller (handlers swallow them silently per spec).
func (s *Service) CheckLatest(ctx context.Context) (Latest, error) {
	tag, err := s.latestClient().resolveLatest(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCheck = time.Now()
	if err != nil {
		s.lastErr = err.Error()
		return Latest{}, err
	}
	s.lastErr = ""
	s.latest = Latest{Tag: tag, Version: strings.TrimPrefix(tag, "v")}
	return s.latest, nil
}

// cachedLatest returns the last resolved latest (may be zero before first check).
func (s *Service) cachedLatest() Latest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// Status builds the UI snapshot from the cache + the manifest.
func (s *Service) Status() Status {
	s.mu.Lock()
	latest, lastCheck, lastErr := s.latest, s.lastCheck, s.lastErr
	s.mu.Unlock()

	// A release that already failed its health-gate is not offered again (until a
	// newer one appears) — otherwise the badge stays lit and clicking Install
	// re-downloads and re-applies the same broken release forever.
	available := latest.Version != "" &&
		Greater(latest.Version, s.current) &&
		!IsVersionFailed(s.updatesDir, latest.Version)
	st := Status{
		CurrentVersion: s.current,
		LatestVersion:  latest.Version,
		Available:      available,
		Error:          lastErr,
		Applying:       s.applying.Load(),
		ApplyError:     FailedNewerThan(s.updatesDir, s.current),
	}
	if !lastCheck.IsZero() {
		st.LastCheckAt = lastCheck.UTC().Format(time.RFC3339)
	}
	return st
}

// Changelog returns notes for (current, latest] in the given locale, refreshing
// the cached latest first if it is unknown.
func (s *Service) Changelog(ctx context.Context, locale string) ([]ReleaseNote, error) {
	latest := s.cachedLatest()
	if latest.Version == "" {
		var err error
		if latest, err = s.CheckLatest(ctx); err != nil {
			return nil, err
		}
	}
	return changelog(ctx, s.dataClient(), s.current, latest, locale)
}

// Stage downloads the latest desktop payload, verifies its sha256, extracts the
// daemon binary into updates/<ver>/citeck-launcher, and records it pending in the
// manifest. Returns the staged version. The full download+verify completes BEFORE
// any swap so a failure never disturbs the running daemon.
func (s *Service) Stage(ctx context.Context) (string, error) {
	if !s.applying.CompareAndSwap(false, true) {
		return "", errors.New("update already in progress")
	}
	defer s.applying.Store(false)

	latest, checkErr := s.CheckLatest(ctx)
	if checkErr != nil {
		return "", checkErr
	}
	if !Greater(latest.Version, s.current) {
		return "", fmt.Errorf("no update available (current %s, latest %s)", s.current, latest.Version)
	}
	// Defense-in-depth: latest.Version comes from a GitHub redirect and is joined
	// into filesystem paths below. Reject anything that is not clean semver so a
	// hostile redirect cannot smuggle path traversal (e.g. "..").
	if !IsValidVersion(latest.Version) {
		return "", fmt.Errorf("refusing unsafe version string %q", latest.Version)
	}
	// Don't re-apply a release that already failed its health-gate; wait for a
	// newer one (prevents an infinite download→apply→rollback loop).
	if IsVersionFailed(s.updatesDir, latest.Version) {
		return "", fmt.Errorf("version %s previously failed to apply; awaiting a newer release", latest.Version)
	}

	c := s.dataClient()
	asset := fmt.Sprintf("citeck-desktop_%s_linux_%s.tar.gz", latest.Version, runtime.GOARCH)
	verDir := filepath.Join(s.updatesDir, latest.Version)
	targz := filepath.Join(verDir, asset)
	binPath := filepath.Join(verDir, "citeck-launcher")

	if err := c.downloadFile(ctx, c.assetURL(latest.Tag, asset), targz); err != nil {
		return "", err
	}
	sumHex, shaErr := fetchExpectedSHA(ctx, c, latest.Tag, asset)
	if shaErr != nil {
		_ = os.Remove(targz)
		return "", shaErr
	}
	if err := verifySHA256(targz, sumHex); err != nil {
		_ = os.Remove(targz)
		return "", err
	}
	if err := extractDaemonBinary(targz, binPath); err != nil {
		_ = os.Remove(targz)
		return "", err
	}
	_ = os.Remove(targz) // keep only the extracted binary

	if err := AddStaged(s.updatesDir, Entry{Version: latest.Version, Path: binPath, SHA256: sumHex}); err != nil {
		return "", err
	}
	if err := MarkState(s.updatesDir, latest.Version, StatePending); err != nil {
		return "", err
	}
	slog.Info("Update staged", "version", latest.Version, "path", binPath)
	return latest.Version, nil
}

// RunPeriodic checks latest now and every periodicInterval until ctx is done.
func (s *Service) RunPeriodic(ctx context.Context) {
	_, _ = s.CheckLatest(ctx)
	ticker := time.NewTicker(periodicInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.CheckLatest(ctx); err != nil {
				slog.Debug("Periodic update check failed", "err", err)
			}
		}
	}
}

// fetchExpectedSHA downloads the asset's .sha256 sidecar (a release asset, not a
// raw repo file) and returns the lowercase hex digest from its first field.
func fetchExpectedSHA(ctx context.Context, c *client, tag, asset string) (string, error) {
	tmp, err := os.CreateTemp("", "sha-*")
	if err != nil {
		return "", fmt.Errorf("temp for sha: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpName)
	if dlErr := c.downloadFile(ctx, c.assetURL(tag, asset+".sha256"), tmpName); dlErr != nil {
		return "", dlErr
	}
	data, err := os.ReadFile(tmpName) //nolint:gosec // our own temp
	if err != nil {
		return "", fmt.Errorf("read sha sidecar: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", errors.New("empty sha256 sidecar")
	}
	return strings.ToLower(fields[0]), nil
}

func verifySHA256(path, expectedHex string) error {
	f, err := os.Open(path) //nolint:gosec // our own download
	if err != nil {
		return fmt.Errorf("open for verify: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, expectedHex)
	}
	return nil
}

// extractDaemonBinary writes the first regular file in the tar.gz (the
// citeck-launcher binary) to dst with mode 0755.
func extractDaemonBinary(targz, dst string) error {
	f, err := os.Open(targz) //nolint:gosec // our own download
	if err != nil {
		return fmt.Errorf("open targz: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		out, openErr := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // executable payload
		if openErr != nil {
			return fmt.Errorf("create dst: %w", openErr)
		}
		// Cap extraction to 200 MiB to bound a malicious tar (the daemon is ~24 MB).
		if _, copyErr := io.Copy(out, io.LimitReader(tr, 200<<20)); copyErr != nil {
			_ = out.Close()
			return fmt.Errorf("extract: %w", copyErr)
		}
		if closeErr := out.Close(); closeErr != nil {
			return fmt.Errorf("close dst: %w", closeErr)
		}
		return nil
	}
	return errors.New("no regular file in payload archive")
}
