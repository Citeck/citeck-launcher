package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
)

func TestEventRing_EmptyReturnsNothing(t *testing.T) {
	r := newEventRing(4)
	got, ok := r.since(0)
	if !ok {
		t.Fatalf("empty ring should be ok")
	}
	if len(got) != 0 {
		t.Fatalf("empty ring should return 0 events, got %d", len(got))
	}
}

func TestEventRing_ReplaysAfterLastSeq(t *testing.T) {
	r := newEventRing(10)
	for i := int64(1); i <= 10; i++ {
		r.push(api.EventDto{Seq: i, Type: "x"})
	}
	got, ok := r.since(5)
	if !ok {
		t.Fatalf("ring within capacity should be ok, want true")
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 events after seq 5, got %d", len(got))
	}
	for i, evt := range got {
		want := int64(6 + i)
		if evt.Seq != want {
			t.Errorf("event[%d].Seq = %d, want %d", i, evt.Seq, want)
		}
	}
}

func TestEventRing_WrapsAndOverwritesOldest(t *testing.T) {
	r := newEventRing(5)
	for i := int64(1); i <= 12; i++ {
		r.push(api.EventDto{Seq: i})
	}
	// Ring holds Seq 8..12 (last 5). Client asking for seq > 9 → 10,11,12.
	got, ok := r.since(9)
	if !ok {
		t.Fatalf("lastSeq=9 with oldest=8 should be ok")
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d (%v)", len(got), got)
	}
	for i, evt := range got {
		want := int64(10 + i)
		if evt.Seq != want {
			t.Errorf("event[%d].Seq = %d, want %d", i, evt.Seq, want)
		}
	}
}

func TestEventRing_GapBeyondBufferSignalsResync(t *testing.T) {
	r := newEventRing(5)
	for i := int64(1); i <= 12; i++ {
		r.push(api.EventDto{Seq: i})
	}
	// Ring holds 8..12; lastSeq=3 is too far behind — oldest=8 > 4.
	_, ok := r.since(3)
	if ok {
		t.Fatalf("expected ok=false (resync) when client is behind the ring window")
	}
}

func TestEventRing_ReplayAcrossDisconnectWindow(t *testing.T) {
	// Scenario from the task: publish 10, "disconnect", reconnect with
	// Last-Event-ID=5, expect events 6..10 replayed.
	r := newEventRing(500)
	for i := int64(1); i <= 10; i++ {
		r.push(api.EventDto{Seq: i, Type: "snapshot_progress"})
	}
	got, ok := r.since(5)
	if !ok || len(got) != 5 {
		t.Fatalf("expected 5 replayed events, got %d (ok=%v)", len(got), ok)
	}
	for i, evt := range got {
		if evt.Seq != int64(6+i) {
			t.Errorf("event[%d].Seq = %d, want %d", i, evt.Seq, int64(6+i))
		}
	}
}

func TestEventRing_LastSeqEqualToLatestNoEvents(t *testing.T) {
	r := newEventRing(10)
	for i := int64(1); i <= 5; i++ {
		r.push(api.EventDto{Seq: i})
	}
	got, ok := r.since(5)
	if !ok {
		t.Fatalf("ok should be true when client is at head")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 replayed events, got %d", len(got))
	}
}
