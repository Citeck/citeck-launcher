package update

import (
	"context"
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

func quickDownloadPolicy() downloadPolicy {
	p := defaultDownloadPolicy()
	p.delay = time.Millisecond
	p.idle = time.Second
	return p
}

func TestUpdateDownloadManyProgressingRetries(t *testing.T) {
	payload := strings.Repeat("x", 110)
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := int(attempts.Add(1)) - 1
		if offset > 0 {
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", offset) {
				t.Errorf("Range=%q offset=%d", got, offset)
			}
			if got := r.Header.Get("If-Range"); got != `"stable"` {
				t.Errorf("If-Range=%q", got)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
		}
		w.Header().Set("ETag", `"stable"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)-offset))
		if offset > 0 {
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write([]byte(payload[offset : offset+1]))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "asset")
	policy := quickDownloadPolicy()
	var previous int64
	if err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, policy, func(received, total int64) {
		if received < previous || total != int64(len(payload)) {
			t.Errorf("progress %d/%d after %d", received, total, previous)
		}
		previous = received
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != payload || attempts.Load() != 110 {
		t.Fatalf("bytes=%d attempts=%d err=%v", len(data), attempts.Load(), err)
	}
}

func TestUpdateDownloadRestartsAndRangeValidation(t *testing.T) {
	for _, mode := range []string{"ignored", "changed", "416", "bad-range", "bad-validator"} {
		t.Run(mode, func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := attempts.Add(1)
				if attempt == 1 {
					w.Header().Set("ETag", `"old"`)
					w.Header().Set("Content-Length", "6")
					_, _ = w.Write([]byte("abc"))
					return
				}
				if attempt == 2 && r.Header.Get("Range") != "bytes=3-" {
					t.Errorf("Range=%q", r.Header.Get("Range"))
				}
				if mode == "416" && attempt == 2 {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if mode == "bad-range" || mode == "bad-validator" {
					header := "bytes 3-5/6"
					if mode == "bad-range" {
						header = "bytes 0-2/6"
					}
					w.Header().Set("Content-Range", header)
					w.Header().Set("ETag", `"changed"`)
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write([]byte("def"))
					return
				}
				if mode == "416" && attempt == 3 && r.Header.Get("Range") != "" {
					t.Errorf("restart Range=%q", r.Header.Get("Range"))
				}
				if mode == "changed" {
					w.Header().Set("ETag", `"new"`)
				}
				_, _ = w.Write([]byte("uvwxyz"))
			}))
			defer srv.Close()
			dst := filepath.Join(t.TempDir(), "asset")
			if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, quickDownloadPolicy())
			want := "uvwxyz"
			if strings.HasPrefix(mode, "bad-") {
				want = "original"
				if err == nil {
					t.Fatal("unsafe range accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			data, readErr := os.ReadFile(dst)
			if readErr != nil || string(data) != want {
				t.Fatalf("data=%q err=%v", data, readErr)
			}
			entries, _ := os.ReadDir(filepath.Dir(dst))
			if len(entries) != 1 {
				t.Fatalf("temporary file leaked: %v", entries)
			}
		})
	}
}

func TestUpdateDownloadFailurePolicy(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { attempts.Add(1); w.WriteHeader(status) }))
			defer srv.Close()
			dst := filepath.Join(t.TempDir(), "asset")
			err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, quickDownloadPolicy())
			want := int32(1)
			if status >= 500 || status == 429 {
				want = 3
			}
			if err == nil || attempts.Load() != want {
				t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
			}
			if status == 404 && !errors.Is(err, errNotFound) {
				t.Fatalf("lost missing-asset classification: %v", err)
			}
		})
	}
}

func TestUpdateDownloadIdleResumeAndCancellation(t *testing.T) {
	t.Run("idle-resume", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) == 1 {
				w.Header().Set("Content-Length", "6")
				_, _ = w.Write([]byte("abc"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if r.Header.Get("Range") != "bytes=3-" {
				t.Errorf("Range=%q", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Range", "bytes 3-5/6")
			w.WriteHeader(206)
			_, _ = w.Write([]byte("def"))
		}))
		defer srv.Close()
		p := quickDownloadPolicy()
		p.idle = 100 * time.Millisecond
		dst := filepath.Join(t.TempDir(), "asset")
		if err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, p); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(dst)
		if string(data) != "abcdef" {
			t.Fatalf("data=%q", data)
		}
	})
	t.Run("cancel-delay", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		requested := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503); close(requested) }))
		defer srv.Close()
		go func() { <-requested; cancel() }()
		err := (&client{http: srv.Client()}).downloadFile(ctx, srv.URL, filepath.Join(t.TempDir(), "asset"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestUpdateDownloadSizeLimit(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if unknown {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write([]byte("oversized"))
			}))
			defer srv.Close()
			p := quickDownloadPolicy()
			p.maxBytes = 3
			dst := filepath.Join(t.TempDir(), "asset")
			err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, p)
			if err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("got %v", err)
			}
			if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
				t.Fatal("oversize payload installed")
			}
		})
	}
}

func TestUpdateDownloadNoProgressAfterPartialStops(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Length", "6")
			_, _ = w.Write([]byte("abc"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "asset")
	err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, quickDownloadPolicy())
	if err == nil || attempts.Load() != 3 {
		t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 0 {
		t.Fatalf("partial download leaked: %v", entries)
	}
}

func TestUpdateDownloadRejectsOverlongPartialResponse(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Length", "6")
			_, _ = w.Write([]byte("abc"))
			return
		}
		// Chunked response claims two bytes but sends three; don't accept it as a
		// completed file just because the received byte count reaches total size.
		w.Header().Set("Content-Range", "bytes 3-4/6")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("def"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "asset")
	err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, quickDownloadPolicy())
	if err == nil || !strings.Contains(err.Error(), "declared range") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("malformed response accepted")
	}
}

func TestUpdateDownloadIgnoredRangeDoesNotResetRetries(t *testing.T) {
	for _, growing := range []bool{false, true} {
		t.Run(fmt.Sprint(growing), func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := int(attempts.Add(1))
				if attempt > 1 && r.Header.Get("Range") == "" {
					t.Error("missing resume request")
				}
				w.Header().Set("Content-Length", "10")
				size := 1
				if growing {
					size = attempt
				}
				_, _ = w.Write([]byte(strings.Repeat("x", size))) // Always 200; never actually resumes.
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			dst := filepath.Join(t.TempDir(), "asset")
			err := (&client{http: srv.Client()}).downloadFileWithPolicy(ctx, srv.URL, dst, quickDownloadPolicy())
			if err == nil || errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 3 {
				t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
			}
			entries, _ := os.ReadDir(filepath.Dir(dst))
			if len(entries) != 0 {
				t.Fatalf("partial leaked: %v", entries)
			}
		})
	}
}

func TestUpdateDownloadRetriesShortChunkedRangeWithUnknownTotal(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("abc"))
			w.(http.Flusher).Flush()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case 2:
			if r.Header.Get("Range") != "bytes=3-" {
				t.Errorf("wrong resume offset")
			}
			w.Header().Set("Content-Range", "bytes 3-5/*")
			w.WriteHeader(http.StatusPartialContent)
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("d")) // clean chunked EOF, but missing two declared bytes
		default:
			if r.Header.Get("Range") != "bytes=4-" {
				t.Errorf("short body was not retained for resume")
			}
			w.Header().Set("Content-Range", "bytes 4-5/6")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("ef"))
		}
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "asset")
	err := (&client{http: srv.Client()}).downloadFileWithPolicy(context.Background(), srv.URL, dst, quickDownloadPolicy())
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(dst)
	if readErr != nil || string(data) != "abcdef" || attempts.Load() != 3 {
		t.Fatalf("short range accepted as complete: bytes=%d attempts=%d err=%v", len(data), attempts.Load(), readErr)
	}
}
