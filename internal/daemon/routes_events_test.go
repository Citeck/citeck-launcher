package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

// TestHandleEvents_ReplaysAfterLastEventID exercises the round-trip:
// publish 10 events, disconnect, reconnect with ?lastSeq=5, assert events
// 6..10 are streamed before any live event.
func TestHandleEvents_ReplaysAfterLastEventID(t *testing.T) {
	d := &Daemon{eventRing: newEventRing(500)}
	for i := int64(1); i <= 10; i++ {
		d.broadcastEvent(api.EventDto{Type: "snapshot_progress", Current: int(i)})
	}

	srv := httptest.NewServer(http.HandlerFunc(d.handleEvents))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?lastSeq=5", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	seqs := readSSESeqs(t, resp.Body, 5, 1500*time.Millisecond)
	if len(seqs) != 5 {
		t.Fatalf("expected 5 replayed events, got %d (%v)", len(seqs), seqs)
	}
	for i, s := range seqs {
		if s != int64(6+i) {
			t.Errorf("seq[%d] = %d, want %d", i, s, int64(6+i))
		}
	}
}

// TestHandleEvents_ResyncWhenBehindRing verifies that a client whose
// lastSeq is older than the ring window receives the resync sentinel.
func TestHandleEvents_ResyncWhenBehindRing(t *testing.T) {
	d := &Daemon{eventRing: newEventRing(5)}
	for i := int64(1); i <= 12; i++ {
		d.broadcastEvent(api.EventDto{Type: "x"})
	}

	srv := httptest.NewServer(http.HandlerFunc(d.handleEvents))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?lastSeq=2", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(1 * time.Second)
	saw := false
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "event: resync") {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("expected 'event: resync' frame when client is behind the ring window")
	}
}

// TestHandleEvents_LiveStreamAfterReplay verifies that events published
// after a reconnect-with-replay still reach the live subscriber.
func TestHandleEvents_LiveStreamAfterReplay(t *testing.T) {
	d := &Daemon{eventRing: newEventRing(500)}
	d.broadcastEvent(api.EventDto{Type: "snapshot_progress"})
	d.broadcastEvent(api.EventDto{Type: "snapshot_progress"})

	srv := httptest.NewServer(http.HandlerFunc(d.handleEvents))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?lastSeq=1", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	// First read the 1 replayed event (seq=2)
	first := readSSESeqs(t, resp.Body, 1, 1*time.Second)
	if len(first) != 1 || first[0] != 2 {
		t.Fatalf("expected replayed seq=2, got %v", first)
	}

	// Wait until the subscriber is registered before publishing, otherwise
	// broadcastEvent fires before addSubscriber returns and the live event
	// is missed (the test would hang).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		d.eventMu.Lock()
		n := len(d.eventSubs)
		d.eventMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	d.broadcastEvent(api.EventDto{Type: "snapshot_complete"})

	live := readSSESeqs(t, resp.Body, 1, 1*time.Second)
	if len(live) != 1 || live[0] != 3 {
		t.Fatalf("expected live seq=3 after replay, got %v", live)
	}
}

// readSSESeqs reads SSE frames until it collects `want` data lines or the
// timeout elapses. Returns the Seq values parsed from each event.
func readSSESeqs(t *testing.T, body interface {
	Read(p []byte) (int, error)
}, want int, timeout time.Duration,
) []int64 {
	t.Helper()
	br := bufio.NewReader(body)
	deadline := time.Now().Add(timeout)
	var seqs []int64
	var lastID int64
	for len(seqs) < want && time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "id: "):
			// captured to confirm id field is emitted (Last-Event-ID source)
			var n int64
			for _, c := range line[len("id: "):] {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int64(c-'0')
			}
			lastID = n
		case strings.HasPrefix(line, "data: "):
			seqs = append(seqs, lastID)
		}
	}
	return seqs
}
