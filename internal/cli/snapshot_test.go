package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/stretchr/testify/require"
)

func TestSnapshotImportFailureNeverRestartsTheNamespace(t *testing.T) {
	// Keep Unix socket paths short even with a long checkout/TMPDIR.
	dir, dirErr := os.MkdirTemp("", "cli-sock-")
	require.NoError(t, dirErr)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)
	previousYes := flagYes
	flagYes = true
	t.Cleanup(func() { flagYes = previousYes })
	listener, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	require.NoError(t, err)
	var stopped, starts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc(api.Snapshots, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.SnapshotDto{{Name: "fixture.zip"}})
	})
	mux.HandleFunc(api.Namespace, func(w http.ResponseWriter, _ *http.Request) {
		status := api.NsStatusRunning
		if stopped.Load() != 0 {
			status = api.NsStatusStopped
		}
		_ = json.NewEncoder(w).Encode(api.NamespaceDto{Status: status})
	})
	mux.HandleFunc(api.NamespaceStop, func(w http.ResponseWriter, _ *http.Request) {
		stopped.Store(1)
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true})
	})
	mux.HandleFunc(api.NamespaceStart, func(w http.ResponseWriter, _ *http.Request) {
		starts.Add(1)
		_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true})
	})
	mux.HandleFunc(api.Events, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc(api.SnapshotsImport, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"fixture import failed after data write"}`))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	command := newSnapshotImportCmd()
	require.Error(t, command.RunE(command, []string{"fixture"}))
	require.Equal(t, int32(1), stopped.Load())
	require.Zero(t, starts.Load(), "restored data may be incomplete")
}
