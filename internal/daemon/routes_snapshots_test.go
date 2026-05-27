package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// newSnapshotsTestDaemon stands up a minimal Daemon for snapshot route tests:
// SQLite-backed store, isolated CITECK_HOME, configured nsConfig + workspaceID
// so snapshotsDir() resolves to a writable temp path. dockerClient stays nil
// — most error paths we exercise short-circuit before any Docker call.
//
// In server mode `ResolveVolumesBase` ignores wsID and uses DataDir() — that
// works fine because t.Setenv("CITECK_HOME") isolates the test FS.
func newSnapshotsTestDaemon(t *testing.T) (*Daemon, *http.ServeMux, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	d := &Daemon{
		store:       store,
		workspaceID: "ws-test",
		nsConfig:    &namespace.Config{ID: "ns-test", Name: "Test NS"},
		bgCtx:       ctx,
		bgCancel:    cancel,
	}

	// Pre-create the snapshots directory so handlers that read it (list, delete)
	// don't trip on missing-dir paths we'd rather not test here.
	snapDir := filepath.Join(config.ResolveVolumesBase(d.workspaceID, d.nsConfig.ID), "snapshots")
	require.NoError(t, os.MkdirAll(snapDir, 0o755))

	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, snapDir
}

// --- ListSnapshots ---------------------------------------------------------

func TestSnapshotList_Empty(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", api.Snapshots, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []api.SnapshotDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Empty(t, list)
}

func TestSnapshotList_PicksUpZipsOnly(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)

	// Seed a mix of zips and noise: only the .zip files should appear.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "alpha.zip"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "beta.zip"), []byte("bb"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "notes.txt"), []byte("n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(snapDir, "subdir"), 0o755))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", api.Snapshots, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []api.SnapshotDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name)
	}
	assert.ElementsMatch(t, []string{"alpha.zip", "beta.zip"}, names)
}

func TestSnapshotList_NoNamespaceConfigured_ReturnsEmpty(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := &Daemon{store: store} // no nsConfig → snapshotsDir errors
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", api.Snapshots, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", strings.TrimSpace(rec.Body.String()))
}

// --- ExportSnapshot --------------------------------------------------------

func TestSnapshotExport_NoDockerClient(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsExport, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "docker client not available")
}

func TestSnapshotExport_InvalidName_Rejected(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)
	// Non-nil docker client so we reach the name-validation branch (the
	// handler guards on dockerClient first; we never invoke any method on it
	// because validation fails before the export goroutine is dispatched).
	d.dockerClient = &docker.Client{}

	cases := []string{
		"bad name with spaces",
		"../traversal",
		"slash/inside",
		"semi;colon",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				api.SnapshotsExport+"?name="+url.QueryEscape(name), http.NoBody)
			mux.ServeHTTP(rec, req)
			// dockerClient nil → 503 wins unless name validation fires first.
			// Validation runs before the dockerClient check, so we expect 400.
			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "snapshot name must contain")
		})
	}
}

func TestSnapshotExport_NonAbsOutput_Rejected(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)
	d.dockerClient = &docker.Client{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsExport+"?output=relative/path", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "output path must be absolute")
}

func TestSnapshotExport_DuplicateName_Rejected(t *testing.T) {
	d, mux, snapDir := newSnapshotsTestDaemon(t)
	d.dockerClient = &docker.Client{}

	// Pre-seed a file the export would try to create.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "dup.zip"), []byte("x"), 0o644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsExport+"?name=dup.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already exists")
}

func TestSnapshotExport_ConcurrentInProgress(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)

	// Simulate another snapshot op holding the mutex.
	d.snapshotMu.Lock()
	t.Cleanup(func() {
		// Try to release; if another path already did, ignore.
		defer func() { _ = recover() }()
		d.snapshotMu.Unlock()
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsExport, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeSnapshotInProgress, body.Code)
}

// --- ImportSnapshot --------------------------------------------------------

func TestSnapshotImport_InvalidName_Rejected(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	cases := []struct {
		name string
		want string
	}{
		{"missing .zip suffix", "no-suffix"},
		{"path traversal in name", "../etc/passwd.zip"},
		{"slash in name", "foo/bar.zip"},
		{"backslash in name", "foo\\bar.zip"},
		{"empty base", ".zip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				api.SnapshotsImport+"?name="+url.QueryEscape(c.want), http.NoBody)
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "invalid snapshot name")
		})
	}
}

func TestSnapshotImport_NameNotFound(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsImport+"?name=does-not-exist.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "snapshot not found")
}

func TestSnapshotImport_NamedNoDocker(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)

	// Seed a fake snapshot file so name-validation + existence check pass.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "real.zip"), []byte("data"), 0o644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsImport+"?name=real.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "docker client not available")
}

func TestSnapshotImport_ConcurrentInProgress(t *testing.T) {
	d, mux, snapDir := newSnapshotsTestDaemon(t)
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "real.zip"), []byte("data"), 0o644))

	d.snapshotMu.Lock()
	t.Cleanup(func() {
		defer func() { _ = recover() }()
		d.snapshotMu.Unlock()
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsImport+"?name=real.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	var body api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.ErrCodeSnapshotInProgress, body.Code)
}

func TestSnapshotImport_UploadMissingFile(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)
	d.dockerClient = &docker.Client{}

	// Empty multipart body — ParseMultipartForm will reject.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsImport, http.NoBody)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- DownloadSnapshot ------------------------------------------------------

func TestSnapshotDownload_InvalidJSON(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsDownload, strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid request body")
}

func TestSnapshotDownload_EmptyURL(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	body, _ := json.Marshal(api.SnapshotDownloadDto{URL: ""})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsDownload, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "url is required")
}

func TestSnapshotDownload_SSRFBlocked(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	cases := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"ftp scheme", "ftp://example.com/foo.zip"},
		{"loopback v4", "http://127.0.0.1/snap.zip"},
		{"loopback name", "http://localhost/snap.zip"},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/"},
		{"private 10.x", "http://10.0.0.1/snap.zip"},
		{"private 192.168.x", "http://192.168.1.1/snap.zip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(api.SnapshotDownloadDto{URL: c.url})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", api.SnapshotsDownload, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			var errResp api.ErrorDto
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
			assert.Equal(t, api.ErrCodeSSRFBlocked, errResp.Code)
		})
	}
}

func TestSnapshotDownload_AlreadyExists_Skips(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)

	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "cached.zip"), []byte("x"), 0o644))

	body, _ := json.Marshal(api.SnapshotDownloadDto{
		URL: "https://example.com/cached.zip", Name: "cached.zip",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsDownload, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp api.ActionResultDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "already exists")
}

func TestSnapshotDownload_NameSanitization(t *testing.T) {
	// Verifies the safeSnapshotFileName + filepath.Base path-traversal strip.
	// We don't trigger an actual download — instead we pre-seed the
	// resolved destination so the "already exists" fast-path fires and we
	// can read the canonical file name back via the response message.
	_, mux, snapDir := newSnapshotsTestDaemon(t)

	// Place a file named "real.zip" inside the snapshots dir. A malicious
	// `Name` of "../foo/real.zip" should be reduced to "real.zip" by
	// filepath.Base — which means the existing file is found and the
	// fast-path skip kicks in. If sanitization were missing the handler
	// would either attempt to write outside snapDir or fail to find the file.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "real.zip"), []byte("x"), 0o644))

	body, _ := json.Marshal(api.SnapshotDownloadDto{
		URL:  "https://example.com/foo.zip",
		Name: "../foo/real.zip",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsDownload, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "real.zip already exists")
}

func TestSnapshotDownload_AppendsZipSuffix(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	// Pre-seed "myname.zip" so the fast-path runs and we observe the suffix.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "myname.zip"), []byte("x"), 0o644))

	body, _ := json.Marshal(api.SnapshotDownloadDto{
		URL:  "https://example.com/x.zip",
		Name: "myname", // no .zip
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.SnapshotsDownload, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "myname.zip already exists")
}

// --- WorkspaceSnapshots ----------------------------------------------------

func TestWorkspaceSnapshots_EmptyConfig(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", api.WorkspaceSnapshots, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []bundle.SnapshotDef
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Empty(t, list)
}

func TestWorkspaceSnapshots_PopulatedConfig(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)

	d.workspaceConfig = &bundle.WorkspaceConfig{
		Snapshots: []bundle.SnapshotDef{
			{ID: "demo", Name: "Demo snapshot", URL: "https://example.com/demo.zip"},
			{ID: "qa", Name: "QA snapshot", URL: "https://example.com/qa.zip"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", api.WorkspaceSnapshots, http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var list []bundle.SnapshotDef
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	assert.Equal(t, "demo", list[0].ID)
	assert.Equal(t, "qa", list[1].ID)
}

// --- DeleteSnapshot --------------------------------------------------------

func TestSnapshotDelete_Success(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	path := filepath.Join(snapDir, "gone.zip")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/snapshots/gone.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file should be gone")
}

func TestSnapshotDelete_NotFound(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/snapshots/missing.zip", http.NoBody)
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "snapshot not found")
}

func TestSnapshotDelete_InvalidName_Rejected(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)

	// Sentinel file at parent level — if path traversal were possible the
	// handler could reach it. We assert it survives every malformed request.
	sentinel := filepath.Join(filepath.Dir(snapDir), "sentinel.zip")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o644))
	t.Cleanup(func() { _ = os.Remove(sentinel) })

	cases := []struct {
		name    string
		urlPath string
	}{
		{"no zip suffix", "/api/v1/snapshots/bar"},
		// Note: ".." segments collapse before reaching the route. Hitting the
		// route at all already implies the path component is a plain name.
		{"name starting with dot", "/api/v1/snapshots/.hidden.zip"},
		{"name with space", "/api/v1/snapshots/bad%20name.zip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("DELETE", c.urlPath, http.NoBody)
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "invalid snapshot name")
		})
	}

	// Sentinel survived.
	_, err := os.Stat(sentinel)
	assert.NoError(t, err)
}

// --- RenameSnapshot --------------------------------------------------------

func TestSnapshotRename_Success(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	oldPath := filepath.Join(snapDir, "before.zip")
	require.NoError(t, os.WriteFile(oldPath, []byte("x"), 0o644))

	body, _ := json.Marshal(map[string]string{"name": "after.zip"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/snapshots/before.zip", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	_, err := os.Stat(filepath.Join(snapDir, "after.zip"))
	require.NoError(t, err)
	_, err = os.Stat(oldPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSnapshotRename_AppendsZipSuffix(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "src.zip"), []byte("x"), 0o644))

	body, _ := json.Marshal(map[string]string{"name": "no-suffix"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/snapshots/src.zip", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	_, err := os.Stat(filepath.Join(snapDir, "no-suffix.zip"))
	assert.NoError(t, err)
}

func TestSnapshotRename_TargetExists_Conflict(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "src.zip"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "dst.zip"), []byte("y"), 0o644))

	body, _ := json.Marshal(map[string]string{"name": "dst.zip"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/snapshots/src.zip", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already exists")
}

func TestSnapshotRename_SourceNotFound(t *testing.T) {
	_, mux, _ := newSnapshotsTestDaemon(t)

	body, _ := json.Marshal(map[string]string{"name": "after.zip"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/snapshots/missing.zip", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "snapshot not found")
}

func TestSnapshotRename_InvalidNames(t *testing.T) {
	_, mux, snapDir := newSnapshotsTestDaemon(t)
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "src.zip"), []byte("x"), 0o644))

	cases := []struct {
		desc     string
		urlPath  string
		bodyJSON string
		want     string
	}{
		{"missing suffix on old name", "/api/v1/snapshots/src", `{"name":"dst.zip"}`, "invalid snapshot name"},
		{"empty new name", "/api/v1/snapshots/src.zip", `{"name":""}`, "missing new name"},
		{"malformed json body", "/api/v1/snapshots/src.zip", `{not-json`, "missing new name"},
		{"unsafe new base", "/api/v1/snapshots/src.zip", `{"name":"-bad.zip"}`, "invalid new name"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("PUT", c.urlPath, strings.NewReader(c.bodyJSON))
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), c.want)
		})
	}
}

// --- isBlockedIP / validateSnapshotURL low-level checks --------------------

func TestIsBlockedIP_Coverage(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // AWS metadata
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			assert.Equal(t, c.blocked, isBlockedIP(net.ParseIP(c.ip)),
				"isBlockedIP(%s)", c.ip)
		})
	}
}

func TestValidateSnapshotURL_Schemes(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"ftp rejected", "ftp://example.com/x.zip", "scheme"},
		{"file rejected", "file:///etc/passwd", "scheme"},
		{"empty host rejected", "http:///foo.zip", "empty hostname"},
		{"malformed url", "://bad", "invalid URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSnapshotURL(c.url)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

func TestSafeSnapshotFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/foo.zip", "foo.zip"},
		{"https://example.com/path/foo.zip?token=abc", "foo.zip"},
		{"https://example.com/", "snapshot.zip"},
		{"no-slash-at-all", "snapshot.zip"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, safeSnapshotFileName(c.in))
		})
	}
}

// --- Concurrency smoke: snapshotMu serializes export/import ---------------

// TestSnapshot_MutexSerializes spins up two parallel requests to the export
// endpoint with a held mutex and asserts both either succeed or return 409 —
// never a panic / nil-pointer in the validation path. Belt-and-braces for the
// snapshotMu invariant documented in the handler.
func TestSnapshot_MutexSerializes(t *testing.T) {
	d, mux, _ := newSnapshotsTestDaemon(t)

	d.snapshotMu.Lock()
	defer d.snapshotMu.Unlock()

	const N = 4
	var wg sync.WaitGroup
	codes := make([]int, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", api.SnapshotsExport, http.NoBody)
			mux.ServeHTTP(rec, req)
			codes[idx] = rec.Code
		}(i)
	}
	wg.Wait()

	for _, code := range codes {
		assert.Equal(t, http.StatusConflict, code, "all should hit mutex 409 while held")
	}
}
