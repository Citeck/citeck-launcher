package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// httpClient is a shared client with reasonable timeouts for large downloads.
var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
	// No overall Timeout — downloads can be large and take minutes.
	// Context cancellation is the proper way to abort.
}

// Kotlin parity retry policy:
//
//	REPEATS_LIMIT_TOTAL = 100 — absolute upper bound on retry attempts
//	REPEATS_LIMIT_WITHOUT_PROGRESS = 3 — stall guard; resets on byte progress
//	REPEAT_DELAY_MS = 3000 — constant sleep between retries
const (
	retriesLimitTotal           = 100
	retriesLimitWithoutProgress = 3
	retryDelay                  = 3 * time.Second
)

// ErrSHA256Mismatch is returned by DownloadWithRetry when the downloaded
// file's hash does not match the expected SHA-256 digest. Callers (notably
// the snapshot import paths) use errors.Is to skip the retry loop on this
// error — a hash mismatch is deterministic, not transient.
var ErrSHA256Mismatch = errors.New("sha256 mismatch")

// ProgressFunc is called periodically during download with bytes received and total size.
// Total may be -1 if the server does not send Content-Length.
type ProgressFunc func(received, total int64)

// DownloadWithRetry wraps a resumable download in the Kotlin-parity retry
// loop. Two independent counters protect against runaway attempts:
//
//  1. retriesTotal — capped at retriesLimitTotal (100). Absolute ceiling.
//  2. retriesWithoutProgress — capped at retriesLimitWithoutProgress (3).
//     Resets to 0 whenever ANY byte is appended to the .part file, so a
//     long but steadily-progressing download can survive many transient
//     failures while a stalled-from-the-start connection fails fast.
//
// Sleep between retries is constant (retryDelay = 3s; matches Kotlin
// REPEAT_DELAY_MS). The context is honored both during sleep and within
// the underlying transfer. Hash-mismatch errors (errors.Is(err, ErrSHA256Mismatch))
// are deterministic — never retried.
//
// progress is invoked from inside the transfer; the retry counter inspects
// the .part file size to detect progress between attempts, so callers do
// not need to wire up additional bookkeeping.
func DownloadWithRetry(ctx context.Context, client *http.Client, rawURL, destPath, expectedSHA256 string, progress ProgressFunc) error {
	if client == nil {
		client = httpClient
	}

	partPath := destPath + ".part"

	var (
		retriesTotal           int
		retriesWithoutProgress int
		lastErr                error
		lastPartSize           int64
	)

	if info, err := os.Stat(partPath); err == nil {
		lastPartSize = info.Size()
	}

	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("download %s: %w", rawURL, errors.Join(err, lastErr))
			}
			return fmt.Errorf("download %s: %w", rawURL, err)
		}

		err := download(ctx, client, rawURL, destPath, expectedSHA256, progress)
		if err == nil {
			return nil
		}
		lastErr = err

		// Hash mismatch is deterministic — re-downloading the same bytes
		// would yield the same mismatch. Surface immediately so callers
		// (handleDownloadSnapshot, downloadAndImportSnapshot) can rename
		// the stale file and report the failure.
		if errors.Is(err, ErrSHA256Mismatch) {
			return err
		}

		retriesTotal++

		curPartSize := lastPartSize
		if info, statErr := os.Stat(partPath); statErr == nil {
			curPartSize = info.Size()
		}
		if curPartSize > lastPartSize {
			// Any forward progress resets the stall counter — a long but
			// steadily-progressing download can survive many transient
			// failures, while a connection that fails before producing any
			// bytes hits retriesLimitWithoutProgress fast.
			retriesWithoutProgress = 0
			lastPartSize = curPartSize
		} else {
			retriesWithoutProgress++
		}

		slog.Warn("Snapshot download attempt failed",
			"url", rawURL,
			"retriesTotal", retriesTotal,
			"retriesWithoutProgress", retriesWithoutProgress,
			"err", err,
		)

		if retriesTotal >= retriesLimitTotal {
			return fmt.Errorf("download %s: exceeded total retry limit (%d): %w", rawURL, retriesLimitTotal, err)
		}
		if retriesWithoutProgress >= retriesLimitWithoutProgress {
			return fmt.Errorf("download %s: exceeded retry-without-progress limit (%d): %w", rawURL, retriesLimitWithoutProgress, err)
		}

		// Constant delay; context-aware sleep so cancellation is responsive.
		select {
		case <-ctx.Done():
			return fmt.Errorf("download %s: %w", rawURL, errors.Join(ctx.Err(), err))
		case <-time.After(retryDelay):
		}
	}
}

func download(ctx context.Context, client *http.Client, rawURL, destPath, expectedSHA256 string, progress ProgressFunc) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: snapshot directory needs 0o755 for Docker volume access
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	partPath := destPath + ".part"

	// Check for existing partial download
	var offset int64
	if info, err := os.Stat(partPath); err == nil {
		offset = info.Size()
	}

	resp, err := doGet(ctx, client, rawURL, offset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// If server returned 200 OK (not 206), we got the full file — reset offset
	if resp.StatusCode == http.StatusOK {
		offset = 0
	}

	// Total size for progress
	total := int64(-1)
	if resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	// Open file for writing
	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	f, err := os.OpenFile(partPath, flags, 0o644) //nolint:gosec // G302: snapshot files need 0o644 for readability
	if err != nil {
		return fmt.Errorf("open %s: %w", partPath, err)
	}

	received := offset
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				_ = f.Close()
				return fmt.Errorf("write %s: %w", partPath, wErr)
			}
			received += int64(n)
			if progress != nil {
				progress(received, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return fmt.Errorf("read body: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", partPath, err)
	}

	// Verify SHA256 if provided
	if expectedSHA256 != "" {
		actual, err := FileSHA256(partPath)
		if err != nil {
			return fmt.Errorf("compute sha256: %w", err)
		}
		if !strings.EqualFold(actual, expectedSHA256) {
			// Stash the corrupt .part as an `_outdated_<ts>.part` so an
			// operator can inspect it (Kotlin parity: see Task 2 rename
			// helper). Fall back to delete if rename fails — keeping a
			// known-bad .part would prevent the next retry from clean
			// re-download.
			if renameErr := stashOutdatedFile(partPath); renameErr != nil {
				slog.Debug("Stash outdated .part failed; removing", "path", partPath, "err", renameErr)
				_ = os.Remove(partPath)
			}
			return fmt.Errorf("%w: expected %s, got %s", ErrSHA256Mismatch, expectedSHA256, actual)
		}
	}

	// Rename .part to final path
	if err := os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("rename %s → %s: %w", partPath, destPath, err)
	}

	return nil
}

// stashOutdatedFile renames a stale file to "<base>_outdated_<YYYYMMDD_HHMMSS><ext>"
// in the same directory so the failed download isn't silently deleted (Kotlin parity:
// "rename old to <name>_outdated_<datetime>.zip"). The format
// preserves the original extension (.zip, .part, etc.) so naive directory listings
// still group the files. Returns an error if the rename fails; callers should fall
// back to delete in that case (better to lose the bad file than to leave it where
// the next attempt will refuse to re-download).
func stashOutdatedFile(path string) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stamp := time.Now().Format("20060102_150405")

	// Preserve the original extension. For multi-suffix files like "snap.zip.part",
	// we use only the final extension — "snap.zip_outdated_<ts>.part" — to match
	// the Kotlin layout where the active suffix stays at the tail.
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	newName := stem + "_outdated_" + stamp + ext
	if err := os.Rename(path, filepath.Join(dir, newName)); err != nil {
		return fmt.Errorf("stash outdated file: %w", err)
	}
	return nil
}

// StashOutdatedFile is exported so callers that perform their own SHA-256
// verification (e.g. the daemon's cached-snapshot pre-check) can preserve a
// stale file with the same naming convention used by Download() failures.
// Falls back to os.Remove via the caller if rename fails.
func StashOutdatedFile(path string) error {
	return stashOutdatedFile(path)
}

// doGet performs an HTTP GET, handling Range requests and 416 retries.
// Returns a single response that the caller must close.
func doGet(ctx context.Context, client *http.Client, rawURL string, offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp, nil
	case http.StatusRequestedRangeNotSatisfiable:
		// Range not satisfiable — close and retry from scratch with a fresh request
		_ = resp.Body.Close()
		retryReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("create retry request: %w", err)
		}
		resp, err = client.Do(retryReq)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", rawURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
		}
		return resp, nil
	default:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
}

// validateURL ensures only http/https URLs are accepted.
func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("snapshot URL is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q, only http/https allowed", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

// FileSHA256 computes the SHA256 hex digest of a file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal snapshot path
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
