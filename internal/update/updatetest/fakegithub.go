// Package updatetest provides a reusable in-memory fake of the GitHub surface
// the desktop auto-updater depends on: the `/releases/latest` redirect,
// release-asset downloads with `.sha256` sidecars, and raw changelog files
// (`changelog/index.json` + `changelog/<ver>/<locale>.md`). It lets any package's
// tests exercise the real update.Service end-to-end without touching the network.
//
// Usage:
//
//	fake := updatetest.Start(t, "Citeck/citeck-launcher",
//	    updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "daemon", Notes: map[string]string{"en": "# 2.6.0"}},
//	)
//	svc := update.NewService("2.4.0", t.TempDir(), fake.Options()...)
package updatetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/citeck/citeck-launcher/internal/update"
)

// Release describes one fake GitHub release the harness should serve.
type Release struct {
	Version       string             // bare semver, e.g. "2.6.0" (no leading v)
	Date          string             // changelog/index.json date, e.g. "2026-06-01"
	BinaryContent string             // becomes the single file "citeck-launcher" inside the .tar.gz payload
	Notes         map[string]string  // locale -> markdown; absent locales fall back to "en" in the launcher
	CorruptSHA    bool               // serve a wrong .sha256 sidecar (to exercise verify rejection)
	SignKey       ed25519.PrivateKey // when set, serve a "<asset>.sig" detached ed25519 signature over the tarball
	SigStatus     int                // when non-zero, serve this HTTP status for the ".sig" asset (e.g. 500 = transient fetch failure)
}

// FakeGitHub is a running fake GitHub server. Close is registered via t.Cleanup.
type FakeGitHub struct {
	Repo string
	srv  *httptest.Server

	mu       sync.RWMutex
	latest   string // bare version of the newest release
	releases []Release
}

// assetName mirrors update.Service's asset naming (the server release tarball)
// for the host arch.
func assetName(version string) string {
	return fmt.Sprintf("citeck_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
}

// makeTarGz packs content as a single executable file "citeck".
func makeTarGz(content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(content)
	_ = tw.WriteHeader(&tar.Header{Name: "citeck", Mode: 0o755, Size: int64(len(body))})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// Start launches a fake GitHub serving the given releases for repo (e.g.
// "Citeck/citeck-launcher"). The newest release by semver is what /releases/latest
// redirects to. The server is closed automatically on test cleanup.
func Start(t *testing.T, repo string, releases ...Release) *FakeGitHub {
	t.Helper()
	if len(releases) == 0 {
		t.Fatal("updatetest.Start: need at least one release")
	}
	f := &FakeGitHub{Repo: repo, releases: releases}
	f.recomputeLatest()
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// recomputeLatest refreshes f.latest from f.releases. Callers hold f.mu (or
// have exclusive access during construction).
func (f *FakeGitHub) recomputeLatest() {
	f.latest = ""
	for _, r := range f.releases {
		if f.latest == "" || semver.Compare("v"+r.Version, "v"+f.latest) > 0 {
			f.latest = r.Version
		}
	}
}

// SetRelease replaces (or appends) a release in place and recomputes latest.
// It lets tests change a release mid-flight — e.g. re-sign an asset to
// exercise the "publisher fixed the signing" recovery path, or publish a
// newer release after a signature failure.
func (f *FakeGitHub) SetRelease(r Release) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.releases {
		if f.releases[i].Version == r.Version {
			f.releases[i] = r
			f.recomputeLatest()
			return
		}
	}
	f.releases = append(f.releases, r)
	f.recomputeLatest()
}

// URL is the base URL of the fake server.
func (f *FakeGitHub) URL() string { return f.srv.URL }

// Options returns the update.Service options that point a Service at this fake.
func (f *FakeGitHub) Options() []update.Option {
	return []update.Option{
		update.WithBaseURLs(f.srv.URL, f.srv.URL),
		update.WithRepo(f.Repo),
	}
}

func (f *FakeGitHub) find(version string) (Release, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, r := range f.releases {
		if r.Version == version {
			return r, true
		}
	}
	return Release{}, false
}

func (f *FakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+f.Repo)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.mu.RLock()
	latest := f.latest
	f.mu.RUnlock()
	switch {
	case rest == "/releases/latest":
		http.Redirect(w, r, "/"+f.Repo+"/releases/tag/v"+latest, http.StatusFound)
	case strings.HasPrefix(rest, "/releases/download/"):
		f.handleAsset(w, r, strings.TrimPrefix(rest, "/releases/download/"))
	case strings.HasPrefix(rest, "/v"+latest+"/changelog/"):
		f.handleChangelog(w, r, strings.TrimPrefix(rest, "/v"+latest+"/changelog/"))
	default:
		http.NotFound(w, r)
	}
}

// handleAsset serves /releases/download/v<ver>/<file> where <file> is the asset
// tar.gz or its .sha256 sidecar.
func (f *FakeGitHub) handleAsset(w http.ResponseWriter, r *http.Request, sub string) {
	// sub has the form  v<ver>/<file>
	tag, file, ok := strings.Cut(sub, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	version := strings.TrimPrefix(tag, "v")
	rel, found := f.find(version)
	if !found {
		http.NotFound(w, r)
		return
	}
	targz := makeTarGz(rel.BinaryContent)
	switch file {
	case assetName(version):
		_, _ = w.Write(targz)
	case assetName(version) + ".sha256":
		sum := "deadbeef"
		if !rel.CorruptSHA {
			h := sha256.Sum256(targz)
			sum = hex.EncodeToString(h[:])
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n", sum, assetName(version))
	case assetName(version) + ".sig":
		if rel.SigStatus != 0 {
			http.Error(w, "simulated signature-fetch hiccup", rel.SigStatus)
			return
		}
		if rel.SignKey == nil {
			http.NotFound(w, r) // unsigned release
			return
		}
		_, _ = w.Write(ed25519.Sign(rel.SignKey, targz))
	default:
		http.NotFound(w, r)
	}
}

// handleChangelog serves changelog/index.json and changelog/<ver>/<locale>.md.
func (f *FakeGitHub) handleChangelog(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == "index.json" {
		type entry struct {
			Version string `json:"version"`
			Date    string `json:"date"`
		}
		f.mu.RLock()
		idx := make([]entry, 0, len(f.releases))
		for _, rel := range f.releases {
			idx = append(idx, entry{Version: rel.Version, Date: rel.Date})
		}
		f.mu.RUnlock()
		sort.Slice(idx, func(i, j int) bool {
			return semver.Compare("v"+idx[i].Version, "v"+idx[j].Version) < 0
		})
		_ = json.NewEncoder(w).Encode(idx)
		return
	}
	// sub has the form  <ver>/<locale>.md
	ver, file, ok := strings.Cut(sub, "/")
	if !ok || !strings.HasSuffix(file, ".md") {
		http.NotFound(w, r)
		return
	}
	locale := strings.TrimSuffix(file, ".md")
	rel, found := f.find(ver)
	if !found {
		http.NotFound(w, r)
		return
	}
	md, ok := rel.Notes[locale]
	if !ok {
		http.NotFound(w, r) // absent locale → launcher falls back to en
		return
	}
	_, _ = w.Write([]byte(md))
}
