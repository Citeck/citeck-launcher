package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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

// ProgressFunc is called periodically during download with bytes received and total size.
// Total may be -1 if the server does not send Content-Length.
type ProgressFunc func(received, total int64)

// Download fetches a snapshot ZIP from rawURL, saves it to destPath, and verifies SHA256 if provided.
// Supports resumable downloads via HTTP Range header. Only http/https URLs are accepted.
func Download(ctx context.Context, rawURL, destPath, expectedSHA256 string, progress ProgressFunc) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	partPath := destPath + ".part"

	// Check for existing partial download
	var offset int64
	if info, err := os.Stat(partPath); err == nil {
		offset = info.Size()
	}

	resp, err := doGet(ctx, rawURL, offset)
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
	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", partPath, err)
	}

	received := offset
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				f.Close()
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
			f.Close()
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
			os.Remove(partPath)
			return fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedSHA256, actual)
		}
	}

	// Rename .part to final path
	if err := os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("rename %s → %s: %w", partPath, destPath, err)
	}

	return nil
}

// doGet performs an HTTP GET, handling Range requests and 416 retries.
// Returns a single response that the caller must close.
func doGet(ctx context.Context, rawURL string, offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp, nil
	case http.StatusRequestedRangeNotSatisfiable:
		// Range not satisfiable — close and retry from scratch with a fresh request
		resp.Body.Close()
		retryReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create retry request: %w", err)
		}
		resp, err = httpClient.Do(retryReq)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", rawURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
		}
		return resp, nil
	default:
		resp.Body.Close()
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
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
