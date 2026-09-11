package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// serveFakeDaemonSocket puts an HTTP server on the daemon's Unix socket whose
// /health answers "starting" until booted is set.
func serveFakeDaemonSocket(t *testing.T, booted *atomic.Bool) {
	t.Helper()
	runDir, err := os.MkdirTemp("", "citeck-run")
	require.NoError(t, err)
	t.Setenv("CITECK_RUN", runDir)
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.Health, func(w http.ResponseWriter, _ *http.Request) {
		dto := api.HealthDto{Status: api.HealthStatusStarting}
		if booted.Load() {
			dto = api.HealthDto{Status: "healthy", Healthy: true}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto)
	})
	ln, err := net.Listen("unix", config.SocketPath())
	require.NoError(t, err)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// The daemon now binds its socket BEFORE the slow boot work, so a dial that
// succeeds no longer means the daemon can serve anything. `citeck start` /
// `citeck install` hand the client they get straight to status streaming and
// the install wizard, which would then hit DAEMON_STARTING 503s — so the wait
// must last until the daemon says it has finished booting.
func TestWaitForDaemonWaitsUntilTheDaemonHasFinishedBooting(t *testing.T) {
	var booted atomic.Bool
	serveFakeDaemonSocket(t, &booted)

	type result struct {
		err error
		at  time.Time
	}
	done := make(chan result, 1)
	go func() {
		c, err := waitForDaemon(10*time.Second, false)
		if c != nil {
			c.Close()
		}
		done <- result{err: err, at: time.Now()}
	}()

	select {
	case r := <-done:
		t.Fatalf("waitForDaemon returned while the daemon was still booting (err=%v)", r.err)
	case <-time.After(1500 * time.Millisecond):
	}

	flipped := time.Now()
	booted.Store(true)

	select {
	case r := <-done:
		require.NoError(t, r.err)
		assert.WithinDuration(t, flipped, r.at, 3*time.Second,
			"the wait must end as soon as the daemon is booted")
	case <-time.After(10 * time.Second):
		t.Fatal("waitForDaemon never returned after the daemon finished booting")
	}
}
