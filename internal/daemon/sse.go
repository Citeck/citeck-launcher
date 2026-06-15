package daemon

import (
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/api"
)

// SSE event broadcast: fanout to subscribers plus a bounded replay ring so
// reconnecting clients (Last-Event-ID) can catch up without a full resync.
// The subscriber list, the ring, and the monotonic sequence are all guarded
// by d.eventMu — see broadcastEvent / addSubscriber for the ordering contract.

const maxSSESubscribers = 100

// eventReplayBufferSize caps the ring buffer used by SSE reconnects. ~500 events
// covers typical disconnect windows even under pull-progress bursts; older
// events force the client to do a full resync via the existing gap-detection.
const eventReplayBufferSize = 500

func (d *Daemon) broadcastEvent(evt api.EventDto) {
	// Seq assignment, ring push, and fanout all happen under eventMu so a
	// subscriber added between Add and fanout cannot observe a published seq
	// before the event reaches its channel. Paired with addSubscriber, which
	// snapshots eventSeq under the same lock — that snapshot is the cutoff
	// the replay path uses to avoid duplicating live deliveries.
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	evt.Seq = d.eventSeq.Add(1)
	if d.eventRing != nil {
		d.eventRing.push(evt)
	}
	for _, ch := range d.eventSubs {
		select {
		case ch <- evt:
		default:
			d.sseDropped.Add(1)
			slog.Warn("Event dropped for slow subscriber", "type", evt.Type)
		}
	}
}

// addSubscriber registers a new SSE channel and returns the cutoff Seq for
// replay filtering. Events with Seq <= cutoff were broadcast before the
// channel joined the subscriber list and will NOT arrive on `ch`; events with
// Seq > cutoff are guaranteed to arrive live. Both pieces of state are read
// under eventMu so broadcastEvent cannot interleave between them.
func (d *Daemon) addSubscriber() (ch chan api.EventDto, cutoffSeq int64, ok bool) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	if len(d.eventSubs) >= maxSSESubscribers {
		return nil, 0, false
	}
	ch = make(chan api.EventDto, 256)
	d.eventSubs = append(d.eventSubs, ch)
	return ch, d.eventSeq.Load(), true
}

// sseStats is a point-in-time snapshot of SSE pipeline health, shared by the
// /metrics endpoint and the Diagnostics "Event stream" check so both report
// the same numbers.
type sseStats struct {
	EventSeq    int64 // last assigned sequence number (== total events published)
	Subscribers int   // current subscriber count
	QueueLens   []int // per-subscriber undelivered events sitting in the channel buffer
	RingLen     int   // events currently held in the replay ring
	RingCap     int   // replay ring capacity
	Dropped     int64 // cumulative events dropped due to slow consumers
}

// sseStatsSnapshot reads all SSE health counters under eventMu in one shot —
// the same lock broadcastEvent holds, so seq / subscriber list / queue depths
// are mutually consistent. Tolerates the zero-value test Daemon (nil ring).
func (d *Daemon) sseStatsSnapshot() sseStats {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	stats := sseStats{
		EventSeq:    d.eventSeq.Load(),
		Subscribers: len(d.eventSubs),
		Dropped:     d.sseDropped.Load(),
	}
	if len(d.eventSubs) > 0 {
		stats.QueueLens = make([]int, len(d.eventSubs))
		for i, ch := range d.eventSubs {
			stats.QueueLens[i] = len(ch)
		}
	}
	if d.eventRing != nil {
		stats.RingLen = d.eventRing.len()
		stats.RingCap = d.eventRing.size
	}
	return stats
}

func (d *Daemon) removeSubscriber(ch chan api.EventDto) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	for i, sub := range d.eventSubs {
		if sub == ch {
			d.eventSubs = append(d.eventSubs[:i], d.eventSubs[i+1:]...)
			break
		}
	}
	close(ch)
}
