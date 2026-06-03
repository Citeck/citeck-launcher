package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
	asset := fmt.Sprintf("citeck-desktop_2.6.0_linux_%s.tar.gz", runtime.GOARCH)

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
	staged := filepath.Join(dir, "2.6.0", "citeck-launcher")
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
	asset := fmt.Sprintf("citeck-desktop_2.6.0_linux_%s.tar.gz", runtime.GOARCH)
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
