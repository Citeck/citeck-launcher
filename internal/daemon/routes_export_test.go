package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// newExportTestDaemon stands up a Daemon that knows one app ("emodel") and has
// a real volumesBase on disk, so the export handlers work against real files.
func newExportTestDaemon(t *testing.T) (mux *http.ServeMux, exportDir string) {
	t.Helper()
	base := t.TempDir()
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, planStubDocker{}, base)
	t.Cleanup(rt.Shutdown)
	rt.SetGeneratedDefs([]appdef.ApplicationDef{{Name: "emodel", Image: "ecos-model:1"}})

	d := &Daemon{activeNs: &activeNamespace{runtime: rt, volumesBase: base}}
	mux = http.NewServeMux()
	d.registerRoutes(mux)

	require.NoError(t, namespace.EnsureExportDir(base, "emodel"))
	return mux, namespace.ExportDirFor(base, "emodel")
}

func mustTime(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, rfc3339)
	require.NoError(t, err)
	return ts
}

func writeExportFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func doExport(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	return rec
}

func TestListAppExports_ListsFilesNewestFirst(t *testing.T) {
	mux, dir := newExportTestDaemon(t)
	writeExportFile(t, dir, "old.hprof.gz", []byte("older"))
	newest := writeExportFile(t, dir, "new.hprof.gz", []byte("newer-and-longer"))
	// Explicit mtimes — two files written in the same millisecond would
	// otherwise make the ordering assertion a coin flip.
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.hprof.gz"),
		mustTime(t, "2026-08-01T00:00:00Z"), mustTime(t, "2026-08-01T00:00:00Z")))
	require.NoError(t, os.Chtimes(newest,
		mustTime(t, "2026-08-10T00:00:00Z"), mustTime(t, "2026-08-10T00:00:00Z")))

	rec := doExport(mux, "GET", "/api/v1/apps/emodel/export")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var files []api.ExportFileDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &files))
	require.Len(t, files, 2)
	assert.Equal(t, "new.hprof.gz", files[0].Name, "newest first — the reason anyone looks here is what just happened")
	assert.Equal(t, int64(len("newer-and-longer")), files[0].Size)
	assert.Equal(t, "2026-08-10T00:00:00Z", files[0].Modified)
	// HostPath lets a CLI on the same host move the file instead of streaming
	// a heap-sized download through the daemon.
	assert.Equal(t, newest, files[0].HostPath)
}

// An app with nothing exported yet is an empty list, not an error: the export
// directory is created on container start, so it legitimately may not exist.
func TestListAppExports_MissingDirIsEmptyList(t *testing.T) {
	mux, dir := newExportTestDaemon(t)
	require.NoError(t, os.RemoveAll(dir))

	rec := doExport(mux, "GET", "/api/v1/apps/emodel/export")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}

func TestGetAppExport_StreamsBytesVerbatim(t *testing.T) {
	mux, dir := newExportTestDaemon(t)
	payload := []byte{0x1f, 0x8b, 0x08, 0x00, 0xDE, 0xAD, 0xBE, 0xEF}
	writeExportFile(t, dir, "heap.hprof.gz", payload)

	// Built with the same helper the CLI client uses, so the route pattern and
	// the client's URL cannot drift apart silently.
	rec := doExport(mux, "GET", api.AppExportFile("emodel", "heap.hprof.gz"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, payload, rec.Body.Bytes())
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "8", rec.Header().Get("Content-Length"))
}

func TestDeleteAppExport_RemovesTheFile(t *testing.T) {
	mux, dir := newExportTestDaemon(t)
	path := writeExportFile(t, dir, "heap.hprof.gz", []byte("x"))

	rec := doExport(mux, "DELETE", "/api/v1/apps/emodel/export/heap.hprof.gz")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file must be gone")
}

// The names in this directory come from the CONTAINER, not from the launcher.
// A separator or a traversal segment is refused outright rather than cleaned —
// cleaning-and-hoping is how traversal gets in.
func TestExportRoutes_RejectTraversalAndHiddenNames(t *testing.T) {
	mux, _ := newExportTestDaemon(t)

	for _, bad := range []string{"%2E%2E", "%2E%2E%2Fsecrets.yml", "%2Eenv", "..%5Cwin.ini"} {
		for _, method := range []string{"GET", "DELETE"} {
			rec := doExport(mux, method, "/api/v1/apps/emodel/export/"+bad)
			require.Equal(t, http.StatusBadRequest, rec.Code, "%s %s: body=%s", method, bad, rec.Body.String())
			assert.Equal(t, api.ErrCodeInvalidRequest, decodeErr(t, rec).Code)
		}
	}
}

// A symlink is something the CONTAINER can create inside its own writable
// mount, and the daemon usually runs as root — so a link out of the export
// directory must not be served.
func TestGetAppExport_RefusesSymlinkOutOfTheExportDir(t *testing.T) {
	mux, dir := newExportTestDaemon(t)
	outside := filepath.Join(t.TempDir(), "secrets.yml")
	require.NoError(t, os.WriteFile(outside, []byte("master-password: hunter2"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "innocent.txt")))

	rec := doExport(mux, "GET", "/api/v1/apps/emodel/export/innocent.txt")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "hunter2")
}

func TestExportRoutes_UnknownAppIs404(t *testing.T) {
	mux, _ := newExportTestDaemon(t)

	rec := doExport(mux, "GET", "/api/v1/apps/nosuch/export")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, api.ErrCodeAppNotFound, decodeErr(t, rec).Code)

	rec = doExport(mux, "GET", "/api/v1/apps/nosuch/export/heap.hprof.gz")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, api.ErrCodeAppNotFound, decodeErr(t, rec).Code)
}

func TestGetAppExport_MissingFileIs404(t *testing.T) {
	mux, _ := newExportTestDaemon(t)
	rec := doExport(mux, "GET", "/api/v1/apps/emodel/export/absent.hprof.gz")
	require.Equal(t, http.StatusNotFound, rec.Code)
}
