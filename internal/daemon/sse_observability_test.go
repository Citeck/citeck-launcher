package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestMetricsExposeSSEHealth verifies /metrics exposes the SSE pipeline
// counters: event seq, ring occupancy/capacity, subscriber count,
// per-subscriber pending queue, and the cumulative dropped counter.
func TestMetricsExposeSSEHealth(t *testing.T) {
	d := &Daemon{eventRing: newEventRing(8)}
	_, _, ok := d.addSubscriber()
	require.True(t, ok)
	for range 3 {
		d.broadcastEvent(api.EventDto{Type: "app_status"})
	}
	d.sseDropped.Add(2)

	rec := httptest.NewRecorder()
	d.handleMetrics(rec, httptest.NewRequest("GET", "/api/v1/metrics", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "citeck_sse_event_seq 3")
	assert.Contains(t, body, "citeck_sse_ring_events 3")
	assert.Contains(t, body, "citeck_sse_ring_capacity 8")
	assert.Contains(t, body, "citeck_sse_subscribers 1")
	assert.Contains(t, body, `citeck_sse_subscriber_pending{subscriber="0"} 3`)
	assert.Contains(t, body, "citeck_sse_events_dropped_total 2")
}

// TestMetricsSSEHealthZeroState — zero-value pipeline (no ring, no
// subscribers) must still expose the gauges with zeros and omit the
// per-subscriber series entirely.
func TestMetricsSSEHealthZeroState(t *testing.T) {
	d := &Daemon{}

	rec := httptest.NewRecorder()
	d.handleMetrics(rec, httptest.NewRequest("GET", "/api/v1/metrics", http.NoBody))

	body := rec.Body.String()
	assert.Contains(t, body, "citeck_sse_event_seq 0")
	assert.Contains(t, body, "citeck_sse_ring_events 0")
	assert.Contains(t, body, "citeck_sse_ring_capacity 0")
	assert.Contains(t, body, "citeck_sse_subscribers 0")
	assert.Contains(t, body, "citeck_sse_events_dropped_total 0")
	assert.NotContains(t, body, "citeck_sse_subscriber_pending{")
}

func getDiagnosticCheck(t *testing.T, d *Daemon, name string) api.DiagnosticCheckDto {
	t.Helper()
	rec := httptest.NewRecorder()
	d.handleGetDiagnostics(rec, httptest.NewRequest("GET", "/api/v1/diagnostics", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var dto api.DiagnosticsDto
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&dto))
	for _, c := range dto.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("diagnostic check %q not found", name)
	return api.DiagnosticCheckDto{}
}

// TestDiagnosticsEventStreamCheck — the "Event stream" diagnostics row is OK
// with ring occupancy details while nothing was dropped, and degrades to
// "warning" with a reload hint once the slow-consumer drop counter is > 0.
func TestDiagnosticsEventStreamCheck(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	d := testDaemon(t, store)
	d.eventRing = newEventRing(4)
	d.broadcastEvent(api.EventDto{Type: "app_status"})

	check := getDiagnosticCheck(t, d, "Event stream")
	assert.Equal(t, "ok", check.Status)
	assert.Contains(t, check.Message, "ring 1/4")
	assert.Contains(t, check.Message, "seq 1")
	assert.False(t, check.Fixable)

	d.sseDropped.Add(5)
	check = getDiagnosticCheck(t, d, "Event stream")
	assert.Equal(t, "warning", check.Status)
	assert.Contains(t, check.Message, "5 event(s) dropped")
	assert.Contains(t, check.Message, "reload")
}

// TestDiskMonitorEmitsOnStateChangeOnly — the disk monitor broadcasts
// `disk_low` exactly once when free space crosses below the threshold and
// `disk_ok` exactly once on recovery; staying low (or ok) emits nothing.
func TestDiskMonitorEmitsOnStateChangeOnly(t *testing.T) {
	d := &Daemon{eventRing: newEventRing(16)}

	low := d.processDiskSample("/data", 10, 100, false) // ok → ok: no event
	assert.False(t, low)
	low = d.processDiskSample("/data", 3.5, 100, low) // ok → low: disk_low
	assert.True(t, low)
	low = d.processDiskSample("/data", 2.0, 100, low) // low → low: no re-emission
	assert.True(t, low)
	low = d.processDiskSample("/data", 50, 100, low) // low → ok: disk_ok
	assert.False(t, low)
	low = d.processDiskSample("/data", 60, 100, low) // ok → ok: no event
	assert.False(t, low)

	events, ok := d.eventRing.since(0)
	require.True(t, ok)
	require.Len(t, events, 2, "exactly one disk_low + one disk_ok expected")

	assert.Equal(t, "disk_low", events[0].Type)
	assert.Equal(t, "/data", events[0].Path)
	assert.Equal(t, int64(3.5*(1<<30)), events[0].FreeBytes)
	assert.Equal(t, int64(lowDiskWarnGB*(1<<30)), events[0].ThresholdBytes)
	assert.NotZero(t, events[0].Timestamp)

	assert.Equal(t, "disk_ok", events[1].Type)
	assert.Equal(t, "/data", events[1].Path)
	assert.Equal(t, int64(50*(1<<30)), events[1].FreeBytes)
}
