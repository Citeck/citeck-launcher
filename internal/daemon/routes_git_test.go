package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/storage"
)

func postSkipPull(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	req := httptest.NewRequest("POST", "/api/v1/git/skip-pull", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestGitSkipPull_HappyPath records a skip for a host, verifies the git
// package suppresses pulls for it, then clears the skip with a negative
// duration (the GitPullErrorDialog "Skip" → undo round-trip).
func TestGitSkipPull_HappyPath(t *testing.T) {
	host := "skip-pull-test.example"
	// Always clear the process-local skip state, even on assert failure.
	t.Cleanup(func() { git.SkipPullForHost(host, -1) })

	rec := postSkipPull(t, `{"host":"`+host+`","durationSeconds":60}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "skip recorded")
	assert.True(t, git.IsSkipped(host),
		"a recorded skip must suppress pulls for repos on that host")

	// Default duration (0 → DefaultSkipPullDuration) also records a skip.
	rec = postSkipPull(t, `{"host":"`+host+`"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "skip recorded")

	// Negative duration explicitly clears the skip.
	rec = postSkipPull(t, `{"host":"`+host+`","durationSeconds":-1}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "skip cleared")
	assert.False(t, git.IsSkipped(host),
		"a cleared skip must re-enable pulls for that host")
}

// TestGitSyncStoreAdapter_RoundTrip pins the storage↔git type mapping the
// daemon installs via git.SetSyncStateStore at startup: a SetGitRepoState
// write must read back field-for-field, and a missing path yields (nil, nil).
func TestGitSyncStoreAdapter_RoundTrip(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	adapter := gitSyncStoreAdapter{store: store}

	missing, err := adapter.GetGitRepoState("ws/none/bundles/none")
	require.NoError(t, err)
	assert.Nil(t, missing, "absent repo state must be (nil, nil), not an error")

	in := git.SyncStateEntry{Path: "ws/default/bundles/community", LastSyncMs: 1234567, LastCommitHash: "abc123"}
	require.NoError(t, adapter.SetGitRepoState(in))
	got, err := adapter.GetGitRepoState(in.Path)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, in, *got)
}

// TestGitSkipPull_ErrorPaths covers the two 400 branches: malformed JSON and
// a missing host.
func TestGitSkipPull_ErrorPaths(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		rec := postSkipPull(t, `{not-json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid JSON body")
	})
	t.Run("missing host", func(t *testing.T) {
		rec := postSkipPull(t, `{"durationSeconds":60}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "host is required")
	})
}
