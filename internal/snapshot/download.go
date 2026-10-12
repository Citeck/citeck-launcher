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

	"github.com/citeck/citeck-launcher/internal/downloadhttp"
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

// Retry counters measure consecutive failures without new response bytes.
// Only bytes received by a resumed 206 response reset both counters.
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

// DownloadWithRetry resumes interrupted transfers and retries after three seconds.
// Bytes from a resumed 206 response reset all counters; full-file restarts do not.
// Three failures without resumed progress stop the download.
// Hash mismatch is immediate; context cancellation interrupts transfer and delay.
func DownloadWithRetry(ctx context.Context, client *http.Client, rawURL, destPath, expectedSHA256 string, progress ProgressFunc) error {
	return downloadWithRetry(ctx, client, rawURL, destPath, expectedSHA256, progress, retryDelay)
}

func downloadWithRetry(ctx context.Context, client *http.Client, rawURL, destPath, expectedSHA256 string, progress ProgressFunc, delay time.Duration) error {
	if client == nil {
		client = httpClient
	}

	state := &snapshotResumeState{total: -1}
	var (
		retriesTotal           int
		retriesWithoutProgress int
		lastErr                error
	)

	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("download %s: %w", rawURL, errors.Join(err, lastErr))
			}
			return fmt.Errorf("download %s: %w", rawURL, err)
		}

		state.resumedProgress = false
		err := download(ctx, client, rawURL, destPath, expectedSHA256, progress, state)
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

		if state.resumedProgress {
			retriesTotal = 0
			retriesWithoutProgress = 0
		} else {
			retriesTotal++
			retriesWithoutProgress++
		}

		slog.Warn("Snapshot download attempt failed",
			"url", rawURL,
			"retriesTotal", retriesTotal,
			"retriesWithoutProgress", retriesWithoutProgress,
			"err", err,
		)

		if retriesTotal >= retriesLimitTotal {
			return fmt.Errorf("download %s: exceeded consecutive retry limit (%d): %w", rawURL, retriesLimitTotal, err)
		}
		if retriesWithoutProgress >= retriesLimitWithoutProgress {
			return fmt.Errorf("download %s: exceeded retry-without-progress limit (%d): %w", rawURL, retriesLimitWithoutProgress, err)
		}

		// Constant delay; context-aware sleep so cancellation is responsive.
		select {
		case <-ctx.Done():
			return fmt.Errorf("download %s: %w", rawURL, errors.Join(ctx.Err(), err))
		case <-time.After(delay):
		}
	}
}

type snapshotResumeState struct {
	validator       string
	total           int64
	resumedProgress bool
}

type snapshotDownloadResponse struct {
	offset, total, bodyBytes int64
	resumed                  bool
}

func (state *snapshotResumeState) prepareResponse(resp *http.Response, offset int64) (snapshotDownloadResponse, error) {
	response := snapshotDownloadResponse{offset: offset, total: resp.ContentLength, bodyBytes: resp.ContentLength, resumed: offset > 0 && resp.StatusCode == http.StatusPartialContent}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return response, errors.New("unexpected encoded download response")
	}
	validator := downloadhttp.ResumeValidator(resp.Header)
	if resp.StatusCode != http.StatusPartialContent {
		response.offset = 0
		state.validator = validator
		state.total = response.total
		return response, nil
	}
	span, rangeErr := downloadhttp.ValidatePartialResponse(resp, offset)
	if rangeErr != nil {
		return response, fmt.Errorf("invalid snapshot resume: %w", rangeErr)
	}
	if state.total >= 0 && span.Total >= 0 && span.Total != state.total {
		return response, errors.New("snapshot size changed during resume")
	}
	if state.validator != "" && validator != "" && state.validator != validator {
		return response, errors.New("snapshot validator changed during resume")
	}
	response.total = span.Total
	if response.total < 0 && state.total >= 0 {
		response.total = state.total
	}
	response.bodyBytes = span.End - span.Start + 1
	state.total = response.total
	if validator != "" {
		state.validator = validator
	}
	return response, nil
}

func copySnapshotBody(reader io.Reader, f *os.File, response snapshotDownloadResponse, state *snapshotResumeState, progress ProgressFunc) (int64, error) {
	offset, total, responseBytes, resumed := response.offset, response.total, response.bodyBytes, response.resumed
	received := offset
	if progress != nil {
		progress(received, total)
	}
	buf := make([]byte, 64*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if responseBytes >= 0 && int64(n) > responseBytes-(received-offset) {
				return received, errors.New("snapshot body exceeds declared range")
			}
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return received, fmt.Errorf("write snapshot body: %w", wErr)
			}
			received += int64(n)
			if resumed {
				state.resumedProgress = true
			}
			if progress != nil {
				progress(received, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return received, fmt.Errorf("read body: %w", readErr)
		}
	}
	return received, nil
}

func download(ctx context.Context, client *http.Client, rawURL, destPath, expectedSHA256 string, progress ProgressFunc, state *snapshotResumeState) error {
	if state == nil {
		state = &snapshotResumeState{total: -1}
	}
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

	resp, err := doGet(ctx, client, rawURL, offset, state.validator)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	response, responseErr := state.prepareResponse(resp, offset)
	if responseErr != nil {
		return responseErr
	}
	offset, total, responseBytes := response.offset, response.total, response.bodyBytes

	// Open file for writing
	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	f, err := os.OpenFile(partPath, flags, 0o600) //nolint:gosec // G304: internal snapshot path
	if err != nil {
		return fmt.Errorf("open %s: %w", partPath, err)
	}

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("restrict snapshot permissions: %w", err)
	}

	received, copyErr := copySnapshotBody(resp.Body, f, response, state, progress)
	if copyErr != nil {
		_ = f.Close()
		return copyErr
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", partPath, err)
	}

	if responseBytes >= 0 && received-offset != responseBytes {
		return fmt.Errorf("incomplete snapshot body: %w", io.ErrUnexpectedEOF)
	}
	if total >= 0 && received != total {
		return fmt.Errorf("incomplete snapshot: received %d of %d bytes", received, total)
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
func doGet(ctx context.Context, client *http.Client, rawURL string, offset int64, validator string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		if validator != "" {
			req.Header.Set("If-Range", validator)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := downloadhttp.Do(client, req, downloadhttp.IdleTimeout)
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
		retryReq.Header.Set("Accept-Encoding", "identity")
		resp, err = downloadhttp.Do(client, retryReq, downloadhttp.IdleTimeout)
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
