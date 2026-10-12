package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/downloadhttp"
)

type downloadPolicy struct {
	delay, idle       time.Duration
	failures, stalled int
	maxBytes          int64
}

func defaultDownloadPolicy() downloadPolicy {
	return downloadPolicy{delay: 3 * time.Second, idle: downloadhttp.IdleTimeout, failures: 100, stalled: 3, maxBytes: maxDownloadBytes}
}

// downloadFile retains an owned partial file across bounded transport retries.
// Only a complete transfer replaces dst; Stage still verifies hash/signature.
func (c *client) downloadFile(ctx context.Context, srcURL, dst string, progress ...func(received, total int64)) error {
	return c.downloadFileWithPolicy(ctx, srcURL, dst, defaultDownloadPolicy(), progress...)
}

func (c *client) downloadFileWithPolicy(ctx context.Context, srcURL, dst string, policy downloadPolicy, progress ...func(received, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // updater-owned version directory
		return fmt.Errorf("mkdir for download: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dl-*")
	if err != nil {
		return fmt.Errorf("create download temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	transfer := &updateDownload{client: c, file: tmp, policy: policy, total: -1}
	if len(progress) > 0 {
		transfer.progress = progress[0]
	}
	stalled := 0
	for failures := 0; ; {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download canceled: %w", ctxErr)
		}
		transfer.attemptBytes = 0
		retry, attemptErr := transfer.attempt(ctx, srcURL)
		if attemptErr == nil {
			break
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download canceled: %w", ctxErr)
		}
		if !retry {
			return attemptErr
		}
		if transfer.attemptBytes > 0 && transfer.resumed {
			failures = 0
			stalled = 0
		} else {
			failures++
			stalled++
		}
		if failures >= policy.failures || stalled >= policy.stalled {
			return fmt.Errorf("download retry limit reached (%d failures, %d without progress): %w", failures, stalled, attemptErr)
		}
		timer := time.NewTimer(policy.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("download canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close download: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("rename download: %w", err)
	}
	return nil
}

type updateDownload struct {
	attemptBytes    int64
	resumed         bool
	responseBytes   int64
	client          *client
	file            *os.File
	policy          downloadPolicy
	received, total int64
	validator       string
	progress        func(received, total int64)
}

func (d *updateDownload) attempt(ctx context.Context, srcURL string) (bool, error) {
	d.resumed = false
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("build download request: %w", err)
	}
	// Keep byte offsets in the identity representation across retries.
	req.Header.Set("Accept-Encoding", "identity")
	if d.received > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", d.received))
		if d.validator != "" {
			req.Header.Set("If-Range", d.validator)
		}
	}
	resp, requestErr := downloadhttp.Do(d.client.http, req, d.policy.idle)
	if requestErr != nil {
		return true, fmt.Errorf("download request: %w", requestErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("download: %w", errNotFound)
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && d.received > 0 {
		if resetErr := d.reset(); resetErr != nil {
			return false, resetErr
		}
		return true, errors.New("download range no longer available; restarting")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		retry := resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return retry, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if headerErr := d.prepareResponse(resp); headerErr != nil {
		return false, headerErr
	}
	d.resumed = resp.StatusCode == http.StatusPartialContent && req.Header.Get("Range") != ""
	if d.progress != nil {
		d.progress(d.received, d.total)
	}
	writer := &updateDownloadWriter{download: d}
	// Read one byte beyond the cap to reject oversize bodies rather than accepting
	// a truncated payload; resumed offsets count toward the same bound.
	_, copyErr := io.Copy(writer, io.LimitReader(resp.Body, d.policy.maxBytes-d.received+1))
	if writer.err != nil {
		return false, writer.err
	}
	if copyErr != nil {
		return true, fmt.Errorf("read download: %w", copyErr)
	}
	if d.responseBytes >= 0 && d.attemptBytes > d.responseBytes {
		return false, errors.New("download body exceeds declared range")
	}
	if d.responseBytes >= 0 && d.attemptBytes < d.responseBytes {
		return true, fmt.Errorf("incomplete download range: %w", io.ErrUnexpectedEOF)
	}
	if d.total >= 0 && d.received != d.total {
		return true, fmt.Errorf("incomplete download: received %d of %d bytes", d.received, d.total)
	}
	return false, nil
}

func (d *updateDownload) reset() error {
	if err := d.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate download: %w", err)
	}
	if _, err := d.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind download: %w", err)
	}
	d.received = 0
	d.total = -1
	d.validator = ""
	return nil
}

func (d *updateDownload) prepareResponse(resp *http.Response) error {
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return errors.New("unexpected encoded download response")
	}
	validator := downloadhttp.ResumeValidator(resp.Header)
	if resp.StatusCode == http.StatusOK {
		if err := d.reset(); err != nil {
			return err
		}
		d.total = resp.ContentLength
		d.responseBytes = resp.ContentLength
		d.validator = validator

	} else if rangeErr := d.prepareRange(resp, validator); rangeErr != nil {
		return fmt.Errorf("invalid update resume: %w", rangeErr)
	}

	if d.total > d.policy.maxBytes {
		return errors.New("download exceeds size limit")
	}
	return nil
}

func (d *updateDownload) prepareRange(resp *http.Response, validator string) error {
	span, rangeErr := downloadhttp.ValidatePartialResponse(resp, d.received)
	if rangeErr != nil {
		return fmt.Errorf("invalid update resume: %w", rangeErr)
	}
	if d.total >= 0 && span.Total >= 0 && span.Total != d.total {
		return errors.New("download Content-Range does not match requested bytes")
	}
	if d.validator != "" && validator != "" && d.validator != validator {
		return errors.New("download validator changed during resume")
	}
	if span.Total >= 0 || d.total < 0 {
		d.total = span.Total
	}
	d.responseBytes = span.End - span.Start + 1
	if d.validator == "" {
		d.validator = validator
	}
	return nil
}

type updateDownloadWriter struct {
	download *updateDownload
	err      error
}

func (w *updateDownloadWriter) Write(data []byte) (int, error) {
	d := w.download
	if int64(len(data)) > d.policy.maxBytes-d.received {
		w.err = errors.New("download exceeds size limit")
		return 0, w.err
	}
	n, err := d.file.Write(data)
	d.received += int64(n)
	d.attemptBytes += int64(n)
	if n > 0 && d.progress != nil {
		d.progress(d.received, d.total)
	}
	if err != nil {
		w.err = fmt.Errorf("write download: %w", err)
		return n, w.err
	}
	return n, nil
}
