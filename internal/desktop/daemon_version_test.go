package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootingDaemon answers /daemon/status the way a daemon still in its slow boot
// phase does (503 DAEMON_STARTING from the boot socket) for the first
// `bootPolls` requests, then with its version.
func bootingDaemon(t *testing.T, bootPolls int32, version string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.DaemonStatus {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) <= bootPolls {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"` + api.ErrCodeDaemonStarting + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// The auto-update restarts the daemon and asks for its version as soon as the
// restart counts as done — which is LIVENESS, while the daemon is still
// booting and answers every API route with 503 DAEMON_STARTING. Asking once
// and giving up is how the window kept the old version in its title until the
// launcher was restarted by hand. The version is waited for.
func TestRunningDaemonVersionWaitsForTheBootToFinish(t *testing.T) {
	srv, calls := bootingDaemon(t, 3, "2.14.0")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := RunningDaemonVersion(ctx, srv.Client(), srv.URL, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "2.14.0", ver)
	assert.EqualValues(t, 4, calls.Load(), "three refusals, then the answer")
}

func TestRunningDaemonVersionGivesUpAtItsDeadline(t *testing.T) {
	srv, _ := bootingDaemon(t, 1<<30, "never")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := RunningDaemonVersion(ctx, srv.Client(), srv.URL, 10*time.Millisecond)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "bounded by the context, not by the daemon")
}

// A dev build reports "dev-<stamp>"; the title shows the stamp.
func TestRunningDaemonVersionDropsTheDevPrefix(t *testing.T) {
	srv, _ := bootingDaemon(t, 0, "dev-20260923-101500")
	ver, err := RunningDaemonVersion(context.Background(), srv.Client(), srv.URL, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "20260923-101500", ver)
}
