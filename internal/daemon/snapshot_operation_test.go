package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/require"
)

func TestSnapshotOutcomeSurvivesGeneralReplayRingWrap(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{workspaceID: "ws1", nsConfig: &namespace.Config{ID: "ns1"}}, eventRing: newEventRing(eventReplayBufferSize)}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	op := d.newSnapshotOperation("ws1", "ns1")
	op.emit(api.EventDto{Type: "snapshot_complete", NamespaceID: "ns1", After: "imported 5 volumes"})
	for range eventReplayBufferSize + 1 {
		d.broadcastEvent(api.EventDto{Type: "app_stats"})
	}
	_, replayable := d.eventRing.since(1)
	require.False(t, replayable)
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/operations/"+op.id, http.NoBody))
		return rec
	}
	rec := get()
	require.Equal(t, http.StatusOK, rec.Code)
	var event api.EventDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &event))
	require.Equal(t, "snapshot_complete", event.Type)
	require.Equal(t, op.id, event.OperationID)
	require.Equal(t, "imported 5 volumes", event.After)
	d.activeNs.workspaceID = "other-workspace"
	require.Equal(t, http.StatusNotFound, get().Code, "same namespace id in another workspace is not the same operation")
}

func TestSnapshotStatusHistoryBoundsCompletedButKeepsActiveOperations(t *testing.T) {
	d := &Daemon{}
	active := d.newSnapshotOperation("ws", "ns")
	old := d.newSnapshotOperation("ws", "ns")
	old.emit(api.EventDto{Type: "snapshot_error", After: "restore failed"})
	for range snapshotOperationHistory {
		op := d.newSnapshotOperation("ws", "ns")
		op.emit(api.EventDto{Type: "snapshot_complete"})
	}
	require.Contains(t, d.snapshotOps, active.id)
	require.NotContains(t, d.snapshotOps, old.id)
	require.Len(t, d.snapshotOps, snapshotOperationHistory+1)
}

func TestWorkspaceSnapshotPreparationErrorHasRetainedOutcome(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	path := config.WorkspaceDir("ws")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("not a directory"), 0o600))
	d := &Daemon{activeNs: &activeNamespace{workspaceConfig: &bundle.WorkspaceConfig{Snapshots: []bundle.SnapshotDef{{ID: "snapshot"}}}}}
	require.Error(t, d.downloadAndImportSnapshot(nil, "snapshot", "ws", "ns"))
	require.Len(t, d.snapshotOps, 1)
	for _, record := range d.snapshotOps {
		require.Equal(t, "snapshot_error", record.event.Type)
		require.Contains(t, record.event.After, "create snapshot cache")
	}
}

func TestCreateSnapshotStatusIsVisibleBeforeTargetActivation(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	t.Setenv("CITECK_DESKTOP", "false")
	requested, release := make(chan struct{}), make(chan struct{})
	releaseDownload := sync.OnceFunc(func() { close(release) })
	defer releaseDownload()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "controlled snapshot bytes")
	}))
	defer server.Close()
	previousClient := ssrfSafeClient
	ssrfSafeClient = server.Client()
	defer func() { ssrfSafeClient = previousClient }()
	d := &Daemon{bgCtx: context.Background(), activeNs: &activeNamespace{
		workspaceID: "ws", nsConfig: &namespace.Config{ID: "existing"},
		workspaceConfig: &bundle.WorkspaceConfig{Snapshots: []bundle.SnapshotDef{{ID: "snapshot", URL: server.URL}}},
	}}
	done := make(chan error, 1)
	go func() {
		done <- d.importCreateSnapshot("ws", &namespace.Config{ID: "created", Snapshot: "snapshot"})
		close(done)
	}()
	defer func() { releaseDownload(); <-done }()
	<-requested // Real create-time download is in flight, before auto-activation.
	d.snapshotOpsMu.Lock()
	var id string
	for key := range d.snapshotOps {
		id = key
	}
	d.snapshotOpsMu.Unlock()
	require.NotEmpty(t, id)
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/operations/"+id, http.NoBody))
		return rec
	}
	rec := get()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var event api.EventDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &event))
	require.Equal(t, "snapshot_download", event.Type)
	require.Equal(t, "created", event.NamespaceID)
	require.Equal(t, "existing", d.activeNs.nsConfig.ID)
	releaseDownload()
	require.ErrorContains(t, <-done, "docker client not available") // No real volume import.
	require.Equal(t, http.StatusOK, get().Code, "creation failure remains observable from its origin")
	d.activeNs.nsConfig.ID = "unrelated"
	require.Equal(t, http.StatusNotFound, get().Code, "not visible from another namespace")
	d.activeNs.nsConfig.ID = "created"
	require.Equal(t, http.StatusOK, get().Code, "target can read the same outcome after activation")
	d.activeNs.workspaceID = "foreign"
	require.Equal(t, http.StatusNotFound, get().Code, "opaque IDs never bypass workspace isolation")
}
