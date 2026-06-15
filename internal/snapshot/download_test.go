package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testDownload is the former exported single-shot Download API, folded into
// the tests as a thin wrapper around the unexported download(). Production
// callers use DownloadWithRetry; the single-shot behavior is still covered here.
func testDownload(ctx context.Context, rawURL, destPath, expectedSHA256 string, progress ProgressFunc) error {
	return download(ctx, httpClient, rawURL, destPath, expectedSHA256, progress)
}

func TestDownload_Success(t *testing.T) {
	content := []byte("fake-snapshot-zip-content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "test.zip")
	err := testDownload(context.Background(), srv.URL+"/snapshot.zip", dest, "", nil)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", string(got), string(content))
	}
	// Verify .part file was cleaned up
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part file should not exist after successful download")
	}
}

func TestDownload_SHA256Verify(t *testing.T) {
	content := []byte("snapshot-data-for-hash")
	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "verified.zip")
	err := testDownload(context.Background(), srv.URL+"/s.zip", dest, expectedHash, nil)
	if err != nil {
		t.Fatalf("Download with valid SHA256 failed: %v", err)
	}

	// Wrong hash: surface as ErrSHA256Mismatch sentinel + stash bad
	// download as "<base>_outdated_<ts>.part" instead of silently deleting
	// (Kotlin parity). The active .part path must
	// disappear so the next attempt can re-download from scratch.
	dir := t.TempDir()
	dest2 := filepath.Join(dir, "bad.zip")
	err = testDownload(context.Background(), srv.URL+"/s.zip", dest2, "0000000000000000000000000000000000000000000000000000000000000000", nil)
	if err == nil {
		t.Fatal("Download with wrong SHA256 should fail")
	}
	if !errors.Is(err, ErrSHA256Mismatch) {
		t.Errorf("expected ErrSHA256Mismatch, got %v", err)
	}
	if _, err := os.Stat(dest2 + ".part"); !os.IsNotExist(err) {
		t.Error(".part file should not exist after SHA256 mismatch (stashed as _outdated_*.part)")
	}
	// A stashed `bad.zip_outdated_<ts>.part` must exist so operators can inspect the bad bytes.
	entries, _ := os.ReadDir(dir)
	foundStashed := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bad.zip_outdated_") && strings.HasSuffix(e.Name(), ".part") {
			foundStashed = true
			break
		}
	}
	if !foundStashed {
		t.Errorf("expected stashed _outdated_*.part file in %s, found: %v", dir, entries)
	}
}

func TestDownload_Progress(t *testing.T) {
	content := []byte("progress-test-data")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write(content)
	}))
	defer srv.Close()

	var lastReceived int64
	var lastTotal int64
	var calls int
	progress := func(received, total int64) {
		calls++
		if received < lastReceived {
			t.Errorf("received went backwards: %d < %d", received, lastReceived)
		}
		lastReceived = received
		lastTotal = total
	}

	dest := filepath.Join(t.TempDir(), "progress.zip")
	err := testDownload(context.Background(), srv.URL+"/s.zip", dest, "", progress)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if calls == 0 {
		t.Error("progress callback was never called")
	}
	if lastReceived != int64(len(content)) {
		t.Errorf("final received = %d, want %d", lastReceived, len(content))
	}
	if lastTotal != int64(len(content)) {
		t.Errorf("total = %d, want %d", lastTotal, len(content))
	}
}

func TestDownload_Resume(t *testing.T) {
	fullContent := []byte("AAAAABBBBB") // 10 bytes: 5 already downloaded, 5 remaining
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "bytes=5-" {
			w.Header().Set("Content-Range", "bytes 5-9/10")
			w.WriteHeader(http.StatusPartialContent)
			w.Write(fullContent[5:])
		} else {
			w.Write(fullContent)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "resume.zip")

	// Create a partial .part file with first 5 bytes
	partPath := dest + ".part"
	os.WriteFile(partPath, fullContent[:5], 0o644)

	err := testDownload(context.Background(), srv.URL+"/resume.zip", dest, "", nil)
	if err != nil {
		t.Fatalf("Resume download failed: %v", err)
	}

	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, fullContent) {
		t.Errorf("content mismatch: got %q, want %q", string(got), string(fullContent))
	}
}

func TestDownload_ContextCancellation(t *testing.T) {
	// Server that writes slowly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Write([]byte("start"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(5 * time.Second) // slow — will be canceled
		w.Write([]byte("end"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	dest := filepath.Join(t.TempDir(), "canceled.zip")
	err := testDownload(ctx, srv.URL+"/slow.zip", dest, "", nil)
	if err == nil {
		t.Fatal("Download should fail when context is canceled")
	}
}

func TestDownload_InvalidURL(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "test.zip")

	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"file scheme", "file:///etc/passwd"},
		{"ftp scheme", "ftp://example.com/file"},
		{"no host", "http:///path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testDownload(context.Background(), tt.url, dest, "", nil)
			if err == nil {
				t.Errorf("testDownload(%q) should fail", tt.url)
			}
		})
	}
}

func TestDownload_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "notfound.zip")
	err := testDownload(context.Background(), srv.URL+"/missing.zip", dest, "", nil)
	if err == nil {
		t.Fatal("Download of 404 should fail")
	}
}

// TestDownloadWithRetry_RecoversAfterTransientFailures verifies that the
// Kotlin-parity retry loop (100 total / 3 without progress / 3s delay) can
// recover from intermittent connection drops as long as bytes keep arriving.
// First 3 attempts return only partial bytes then drop; 4th completes.
func TestDownloadWithRetry_RecoversAfterTransientFailures(t *testing.T) {
	fullContent := bytes.Repeat([]byte("X"), 16)
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		// Honor Range: server is "resumable" so retry continues from .part offset.
		offset := int64(0)
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			var hi int64
			_, _ = fmt.Sscanf(rng, "bytes=%d-%d", &offset, &hi)
			if offset == 0 {
				_, _ = fmt.Sscanf(rng, "bytes=%d-", &offset)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(fullContent)-1, len(fullContent)))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fullContent)))
		}

		if n < 4 {
			// Send 4 bytes then drop the connection — counts as forward
			// progress so the no-progress counter resets each time.
			chunk := fullContent[offset:]
			if len(chunk) > 4 {
				chunk = chunk[:4]
			}
			w.Write(chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Hijack to forcibly close without sending the rest — simulates EOF mid-body.
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
			return
		}
		// 4th attempt succeeds.
		w.Write(fullContent[offset:])
	}))
	defer srv.Close()

	// Override retry delay via context-friendly path: we use the real const,
	// but with only 4 retries this is fine (12 s in CI is acceptable).
	dest := filepath.Join(t.TempDir(), "retry-recover.zip")
	h := sha256.Sum256(fullContent)
	expected := hex.EncodeToString(h[:])

	err := DownloadWithRetry(context.Background(), nil, srv.URL+"/snap.zip", dest, expected, nil)
	if err != nil {
		t.Fatalf("DownloadWithRetry should recover after transient failures, got: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, fullContent) {
		t.Errorf("content mismatch: got %q, want %q", string(got), string(fullContent))
	}
	if got, want := attempts.Load(), int32(4); got != want {
		t.Errorf("expected exactly %d attempts, got %d", want, got)
	}
}

// TestDownloadWithRetry_StopsAfterNoProgress verifies that the
// retries-without-progress counter (3) trips when bytes never advance.
// Server returns HTTP 500 every time → .part never grows → 3 quick failures.
func TestDownloadWithRetry_StopsAfterNoProgress(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "no-progress.zip")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := DownloadWithRetry(ctx, nil, srv.URL+"/s.zip", dest, "", nil)
	if err == nil {
		t.Fatal("DownloadWithRetry should fail when server never makes progress")
	}
	if !strings.Contains(err.Error(), "retry-without-progress") {
		t.Errorf("expected retry-without-progress error, got: %v", err)
	}
	// Kotlin parity: retries counter starts at REPEATS_LIMIT_WITHOUT_PROGRESS
	// and decrements after each failed attempt — the loop exits when it
	// reaches 0. With zero progress, total attempts = retriesLimitWithoutProgress.
	if got := attempts.Load(); got != int32(retriesLimitWithoutProgress) {
		t.Errorf("expected %d attempts (Kotlin parity: limit-without-progress = total attempts when no progress), got %d",
			retriesLimitWithoutProgress, got)
	}
}

// TestDownloadWithRetry_HashMismatchNotRetried verifies that ErrSHA256Mismatch
// short-circuits the retry loop. A hash mismatch is deterministic — re-downloading
// the same bytes would yield the same mismatch — so retrying wastes time.
func TestDownloadWithRetry_HashMismatchNotRetried(t *testing.T) {
	var attempts atomic.Int32
	content := []byte("hash-mismatch-payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "mismatch.zip")
	err := DownloadWithRetry(context.Background(), nil, srv.URL+"/s.zip", dest, "deadbeef", nil)
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if !errors.Is(err, ErrSHA256Mismatch) {
		t.Errorf("expected ErrSHA256Mismatch, got: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt for hash mismatch (no retry), got %d", got)
	}
}

func TestValidateURL(t *testing.T) {
	valid := []string{
		"http://example.com/file.zip",
		"https://github.com/repo/release.zip",
	}
	for _, u := range valid {
		if err := validateURL(u); err != nil {
			t.Errorf("validateURL(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"",
		"file:///etc/passwd",
		"ftp://example.com",
		"javascript:alert(1)",
		"http:///no-host",
	}
	for _, u := range invalid {
		if err := validateURL(u); err == nil {
			t.Errorf("validateURL(%q) = nil, want error", u)
		}
	}
}
