package git

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLastSuccessfulSyncUsesPersistedTimestampAndDoesNotAdvanceOnFailure(t *testing.T) {
	resetGitState(t)
	t.Cleanup(func() { resetGitState(t) })
	home := t.TempDir()
	dir := filepath.Join(home, "ws", "test", "repo")
	store := newFakeSyncStore()
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	store.entries["ws/test/repo"] = SyncStateEntry{Path: "ws/test/repo", LastSyncMs: stamp}
	SetSyncStateStore(store, home)
	require.Equal(t, stamp, LastSuccessfulSync(dir))
	require.Zero(t, LastSuccessfulSync(filepath.Join(home, "unknown")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	t.Cleanup(srv.Close)
	require.Error(t, CloneOrPullWithAuth(context.Background(), RepoOpts{URL: srv.URL + "/repo.git", Branch: "main", DestDir: dir}))
	require.Equal(t, stamp, LastSuccessfulSync(dir))
	recordSync(RepoOpts{DestDir: dir})
	require.Greater(t, LastSuccessfulSync(dir), stamp)
}
