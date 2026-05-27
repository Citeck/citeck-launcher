package daemon

import (
	"sync"

	"github.com/citeck/citeck-launcher/internal/api"
)

// eventRing is a fixed-capacity ring buffer of recently broadcast events.
// Reconnecting SSE clients use it to replay events missed during the
// disconnect window, identified by Last-Event-ID (= last Seq the client saw).
//
// Capacity is sized to cover typical disconnect windows (≤ ~1 minute) at
// pull/probe burst rates. When the buffer wraps before the client reconnects
// the oldest stored Seq exceeds the client's lastSeq, so the client falls
// back to a full resync via fetchData() — same path as the existing
// gap-detection in store.ts.
type eventRing struct {
	mu   sync.Mutex
	buf  []api.EventDto
	size int
	head int // next write index
	full bool
}

func newEventRing(size int) *eventRing {
	if size <= 0 {
		size = 1
	}
	return &eventRing{
		buf:  make([]api.EventDto, size),
		size: size,
	}
}

func (r *eventRing) push(evt api.EventDto) {
	r.mu.Lock()
	r.buf[r.head] = evt
	r.head++
	if r.head >= r.size {
		r.head = 0
		r.full = true
	}
	r.mu.Unlock()
}

// since returns events with Seq > lastSeq in publish order. If the buffer's
// oldest stored Seq is already > lastSeq+1 (we've wrapped past the gap)
// ok=false signals the caller to advise a full resync. An empty slice with
// ok=true means the client is already up to date.
func (r *eventRing) since(lastSeq int64) (events []api.EventDto, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var start, count int
	if r.full {
		start = r.head
		count = r.size
	} else {
		start = 0
		count = r.head
	}
	if count == 0 {
		return nil, true
	}

	oldest := r.buf[start].Seq
	if oldest > lastSeq+1 {
		return nil, false
	}

	out := make([]api.EventDto, 0, count)
	for i := 0; i < count; i++ {
		evt := r.buf[(start+i)%r.size]
		if evt.Seq > lastSeq {
			out = append(out, evt)
		}
	}
	return out, true
}
