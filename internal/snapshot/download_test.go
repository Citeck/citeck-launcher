package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownload_Success(t *testing.T) {
	content := []byte("fake-snapshot-zip-content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "test.zip")
	err := Download(context.Background(), srv.URL+"/snapshot.zip", dest, "", nil)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
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
	err := Download(context.Background(), srv.URL+"/s.zip", dest, expectedHash, nil)
	if err != nil {
		t.Fatalf("Download with valid SHA256 failed: %v", err)
	}

	// Wrong hash should fail and delete .part file
	dir := t.TempDir()
	dest2 := filepath.Join(dir, "bad.zip")
	err = Download(context.Background(), srv.URL+"/s.zip", dest2, "0000000000000000000000000000000000000000000000000000000000000000", nil)
	if err == nil {
		t.Fatal("Download with wrong SHA256 should fail")
	}
	// .part file should be deleted on hash mismatch
	if _, err := os.Stat(dest2 + ".part"); !os.IsNotExist(err) {
		t.Error(".part file should be deleted after SHA256 mismatch")
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
	err := Download(context.Background(), srv.URL+"/s.zip", dest, "", progress)
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

	err := Download(context.Background(), srv.URL+"/resume.zip", dest, "", nil)
	if err != nil {
		t.Fatalf("Resume download failed: %v", err)
	}

	got, _ := os.ReadFile(dest)
	if string(got) != string(fullContent) {
		t.Errorf("content mismatch: got %q, want %q", got, fullContent)
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
		time.Sleep(5 * time.Second) // slow — will be cancelled
		w.Write([]byte("end"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	dest := filepath.Join(t.TempDir(), "cancelled.zip")
	err := Download(ctx, srv.URL+"/slow.zip", dest, "", nil)
	if err == nil {
		t.Fatal("Download should fail when context is cancelled")
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
			err := Download(context.Background(), tt.url, dest, "", nil)
			if err == nil {
				t.Errorf("Download(%q) should fail", tt.url)
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
	err := Download(context.Background(), srv.URL+"/missing.zip", dest, "", nil)
	if err == nil {
		t.Fatal("Download of 404 should fail")
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
