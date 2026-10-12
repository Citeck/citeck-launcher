package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// makeTarGz returns a gzipped tar containing one file "citeck-launcher".
func makeTarGz(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: "citeck-launcher", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestServiceCheckAndStage(t *testing.T) {
	targz := makeTarGz(t, "new-daemon-binary")
	sum := sha256.Sum256(targz)
	asset := fmt.Sprintf("citeck_2.6.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/Citeck/citeck-launcher/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
	})
	mux.HandleFunc("/Citeck/citeck-launcher/releases/download/v2.6.0/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(targz)
	})
	mux.HandleFunc("/Citeck/citeck-launcher/releases/download/v2.6.0/"+asset+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	svc := NewService("2.4.0", dir)
	// These tests cover staging/state logic, not release signing (which has
	// its own suite) — disable the embedded production key explicitly.
	svc.signingPubKeyHex = ""
	svc.repo = "Citeck/citeck-launcher"
	svc.githubBase = srv.URL
	svc.rawBase = srv.URL
	svc.http = http.DefaultClient
	svc.noRedirectHTTP = noRedirect()
	svc.noRedirectHTTP.Transport = http.DefaultTransport

	st := svc.Status()
	if st.Available {
		t.Fatal("available should be false before first check")
	}

	if _, err := svc.CheckLatest(context.Background()); err != nil {
		t.Fatal(err)
	}
	st = svc.Status()
	if !st.Available || st.LatestVersion != "2.6.0" {
		t.Fatalf("after check: available=%v latest=%q", st.Available, st.LatestVersion)
	}

	version, err := svc.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.6.0" {
		t.Fatalf("staged version = %q", version)
	}
	staged := filepath.Join(dir, "2.6.0", DaemonBinaryName(runtime.GOOS))
	got, _ := os.ReadFile(staged)
	if string(got) != "new-daemon-binary" {
		t.Fatalf("staged binary = %q", got)
	}
	// Manifest entry is pending and selectable.
	if p, ok := SelectBest(dir, "2.4.0"); !ok || p != staged {
		t.Fatalf("SelectBest after stage = %q ok=%v", p, ok)
	}
}

func TestServiceStageRejectsBadChecksum(t *testing.T) {
	targz := makeTarGz(t, "x")
	asset := fmt.Sprintf("citeck_2.6.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/Citeck/citeck-launcher/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
	})
	mux.HandleFunc("/Citeck/citeck-launcher/releases/download/v2.6.0/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(targz)
	})
	mux.HandleFunc("/Citeck/citeck-launcher/releases/download/v2.6.0/"+asset+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", "deadbeef", asset) // wrong hash
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	svc := NewService("2.4.0", dir)
	// These tests cover staging/state logic, not release signing (which has
	// its own suite) — disable the embedded production key explicitly.
	svc.signingPubKeyHex = ""
	svc.repo = "Citeck/citeck-launcher"
	svc.githubBase = srv.URL
	svc.rawBase = srv.URL
	svc.http = http.DefaultClient
	svc.noRedirectHTTP = noRedirect()
	svc.noRedirectHTTP.Transport = http.DefaultTransport

	if _, err := svc.Stage(context.Background()); err == nil {
		t.Fatal("Stage must reject a checksum mismatch")
	}
	// No selectable payload after a rejected stage.
	if _, ok := SelectBest(dir, "2.4.0"); ok {
		t.Fatal("rejected stage must not leave a selectable payload")
	}
}

func TestServiceStageRefusesFailedVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/Citeck/citeck-launcher/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	// Pre-mark 2.6.0 as a payload that already failed its health-gate.
	bin := writeFakeBinary(t, dir, "2.6.0")
	if err := AddStaged(dir, Entry{Version: "2.6.0", Path: bin}); err != nil {
		t.Fatal(err)
	}
	if err := MarkState(dir, "2.6.0", StateFailed); err != nil {
		t.Fatal(err)
	}

	svc := NewService("2.4.0", dir)
	// These tests cover staging/state logic, not release signing (which has
	// its own suite) — disable the embedded production key explicitly.
	svc.signingPubKeyHex = ""
	svc.repo = "Citeck/citeck-launcher"
	svc.githubBase = srv.URL
	svc.rawBase = srv.URL
	svc.http = http.DefaultClient
	svc.noRedirectHTTP = noRedirect()
	svc.noRedirectHTTP.Transport = http.DefaultTransport

	// Stage must refuse to re-download/re-apply a failed release (no infinite loop).
	if _, err := svc.Stage(context.Background()); err == nil {
		t.Fatal("Stage must refuse a version already marked failed")
	}
	// And the failed latest is not offered.
	if svc.Status().Available {
		t.Fatal("Status.Available must be false when latest is a failed version")
	}
}

// Status must expose real bytes while Stage is still blocked on the response body.
func TestStageReportsLiveDownloadProgress(t *testing.T) {
	payload := makeTarGz(t, "verified-daemon")
	sum := sha256.Sum256(payload)
	releaseBody := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseBody) }) }
	mux := http.NewServeMux()
	mux.HandleFunc("/Citeck/citeck-launcher/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
	})
	asset := PayloadAssetName("2.6.0", runtime.GOOS, runtime.GOARCH)
	base := "/Citeck/citeck-launcher/releases/download/v2.6.0/" + asset
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:len(payload)/2])
		w.(http.Flusher).Flush()
		select {
		case <-releaseBody:
			_, _ = w.Write(payload[len(payload)/2:])
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc(base+".sha256", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprintf(w, "%x", sum) })
	server := httptest.NewServer(mux)
	defer server.Close()
	defer release()
	svc := NewService("2.4.0", t.TempDir(), WithBaseURLs(server.URL, server.URL))
	svc.signingPubKeyHex = ""
	done := make(chan error, 1)
	go func() { _, err := svc.Stage(context.Background()); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	var st Status
	for time.Now().Before(deadline) {
		st = svc.Status()
		if st.Progress.ReceivedBytes > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !st.Applying || st.Progress.Phase != "downloading" || st.Progress.ReceivedBytes != int64(len(payload)/2) || st.Progress.TotalBytes != int64(len(payload)) || st.Progress.UpdatedAt < st.Progress.StartedAt {
		t.Fatalf("missing live progress: %+v", st)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if final := svc.Status(); final.Applying || final.Progress.Phase != "ready" {
		t.Fatalf("final status: %+v", final)
	}
}

// Exercise actual staging, including integrity/authenticity verification after
// an interrupted payload, rather than only the downloader helper.
func TestStageResumesDownloadAndVerifiesSignature(t *testing.T) {
	payload := makeTarGz(t, "resumed-signed-daemon")
	hash := sha256.Sum256(payload)
	pub, priv, keyErr := ed25519.GenerateKey(nil)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	signature := ed25519.Sign(priv, payload)
	asset := PayloadAssetName("2.6.0", runtime.GOOS, runtime.GOARCH)
	offset := len(payload) / 2
	var attempts atomic.Int32
	resumed := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	mux := http.NewServeMux()
	mux.HandleFunc("/Citeck/citeck-launcher/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Citeck/citeck-launcher/releases/tag/v2.6.0", http.StatusFound)
	})
	base := "/Citeck/citeck-launcher/releases/download/v2.6.0/" + asset
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"payload"`)
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			_, _ = w.Write(payload[:offset])
			return
		}
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", offset) {
			t.Errorf("Range=%q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)-offset))
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		close(resumed)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(payload[offset:])
	})
	mux.HandleFunc(base+".sha256", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprintf(w, "%x  %s", hash, asset) })
	mux.HandleFunc(base+".sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(signature) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	svc := NewService("2.4.0", dir)
	svc.githubBase = srv.URL
	svc.rawBase = srv.URL
	svc.signingPubKeyHex = hex.EncodeToString(pub)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := svc.Stage(ctx); result <- err }()
	select {
	case <-resumed:
	case err := <-result:
		t.Fatalf("stage ended before resume: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	st := svc.Status()
	if !st.Applying || st.Progress.Phase != "downloading" || st.Progress.ReceivedBytes != int64(offset) {
		t.Errorf("resume progress=%+v", st)
	}
	unblock()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "2.6.0", DaemonBinaryName(runtime.GOOS)))
	if err != nil || string(data) != "resumed-signed-daemon" {
		t.Fatalf("binary=%q err=%v", data, err)
	}
	if st = svc.Status(); st.Applying || st.Progress.Phase != "ready" || st.ManualUpdateRequired {
		t.Fatalf("final=%+v", st)
	}
	if _, ok := SelectBest(dir, "2.4.0"); !ok {
		t.Fatal("verified resumed payload not selectable")
	}
}
