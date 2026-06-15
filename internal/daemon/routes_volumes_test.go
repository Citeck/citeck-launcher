package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/config"
)

// newVolumesTestDaemon mounts the routes for a server-mode Daemon whose
// volumesBase points at a temp dir (so d.volumesDir() = <tmp>/volumes).
func newVolumesTestDaemon(t *testing.T) (mux *http.ServeMux, volDir string) {
	t.Helper()
	config.ResetDesktopMode()
	t.Cleanup(config.ResetDesktopMode)
	base := t.TempDir()
	d := &Daemon{activeNs: &activeNamespace{volumesBase: base}}
	mux = http.NewServeMux()
	d.registerRoutes(mux)
	return mux, filepath.Join(base, "volumes")
}

// TestListVolumes_ServerMode: missing dir → empty list; populated dir → one
// entry per sub-directory (plain files are not volumes).
func TestListVolumes_ServerMode(t *testing.T) {
	mux, volDir := newVolumesTestDaemon(t)

	req := httptest.NewRequest("GET", "/api/v1/volumes", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String(), "missing volumes dir must yield an empty list, not an error")

	require.NoError(t, os.MkdirAll(filepath.Join(volDir, "postgres"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(volDir, "mongodb"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(volDir, "stray-file"), []byte("x"), 0o644))

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/volumes", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var vols []volumeDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &vols))
	require.Len(t, vols, 2, "only directories count as volumes")
	names := []string{vols[0].Name, vols[1].Name}
	assert.ElementsMatch(t, []string{"postgres", "mongodb"}, names)
}

// TestDeleteVolume_Validation covers the deletion guard rails in server mode:
// path-traversal names are rejected, a missing volume 404s, and a present
// volume directory is removed.
func TestDeleteVolume_Validation(t *testing.T) {
	mux, volDir := newVolumesTestDaemon(t)

	t.Run("invalid name rejected", func(t *testing.T) {
		// "@" survives ServeMux routing untouched but fails safeIDPattern, so
		// the request must die in validateAppName before touching the fs.
		req := httptest.NewRequest("DELETE", "/api/v1/volumes/bad@name", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	})

	t.Run("missing volume 404s", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/volumes/ghost", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("existing volume deleted", func(t *testing.T) {
		target := filepath.Join(volDir, "postgres")
		require.NoError(t, os.MkdirAll(target, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(target, "data"), []byte("x"), 0o644))

		req := httptest.NewRequest("DELETE", "/api/v1/volumes/postgres", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		_, statErr := os.Stat(target)
		assert.True(t, os.IsNotExist(statErr), "volume dir must be removed from disk")
	})
}

// TestIsNotFoundErr pins both detection paths used by the idempotent delete:
// the docker errdefs NotFound interface and the plain-message fallback that
// VolumeRemove sometimes returns without implementing the interface.
func TestIsNotFoundErr(t *testing.T) {
	assert.False(t, isNotFoundErr(nil))
	assert.False(t, isNotFoundErr(errors.New("connection refused")))
	assert.True(t, isNotFoundErr(errors.New("Error response from daemon: get foo: no such volume")))
	assert.True(t, isNotFoundErr(fmt.Errorf("wrap: %w", notFoundErr{})))
}

// notFoundErr implements the docker errdefs NotFound contract.
type notFoundErr struct{}

func (notFoundErr) Error() string  { return "not found" }
func (notFoundErr) NotFound() bool { return true }
