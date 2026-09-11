package desktop

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

// serveFakeDaemonHealth puts an HTTP server on the daemon socket whose /health
// answers "starting" until booted is set.
func serveFakeDaemonHealth(t *testing.T, booted *atomic.Bool) {
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

// The health gate must judge the BINARY, not the boot: a daemon that answers
// while it is still starting has proven it runs, which is the only question the
// swap's rollback decision may turn on.
func TestReadyCheckPassesWhileTheDaemonIsStillBooting(t *testing.T) {
	var booted atomic.Bool
	serveFakeDaemonHealth(t, &booted)
	assert.True(t, defaultReadyCheck(), "a booting daemon is a live daemon")
}

// The wrapper's UI gate asks the other question — may I proxy the webview at
// this daemon yet? — and there the boot handler's 503s would land on screen.
func TestDaemonBootedIsFalseUntilTheBootHandlerIsGone(t *testing.T) {
	var booted atomic.Bool
	serveFakeDaemonHealth(t, &booted)
	assert.False(t, DaemonBooted(), "the boot handler cannot serve the UI")
	booted.Store(true)
	assert.True(t, DaemonBooted())
}

// No socket at all is neither alive nor booted.
func TestNeitherCheckPassesWithoutADaemon(t *testing.T) {
	runDir, err := os.MkdirTemp("", "citeck-run")
	require.NoError(t, err)
	t.Setenv("CITECK_RUN", runDir)
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })
	assert.False(t, defaultReadyCheck())
	assert.False(t, DaemonBooted())
}
