package namespace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignalQueueFlushNonBlocking(t *testing.T) {
	s := NewSignalQueue()
	// 10 Flushes in a row must never block, and must coalesce to exactly one
	// pending signal.
	done := make(chan struct{})
	go func() {
		for range 10 {
			s.Flush()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Flush blocked")
	}
	// Drain the single pending signal.
	select {
	case <-s.C():
	default:
		t.Fatal("expected exactly one pending signal after burst")
	}
	// Channel must now be empty.
	select {
	case <-s.C():
		t.Fatal("expected coalescing — found a second pending signal")
	default:
	}
}

func TestSignalQueueDrainBurstExitsOnTimeout(t *testing.T) {
	s := NewSignalQueue()
	start := time.Now()
	s.DrainBurst(context.Background(), 10*time.Millisecond, 4)
	elapsed := time.Since(start)
	// Should exit on first timeout (~10ms) — well below 4*10ms.
	assert.Less(t, elapsed, 50*time.Millisecond, "DrainBurst should exit on first idle timeout")
}

func TestSignalQueueDrainBurstConsumesBurst(t *testing.T) {
	s := NewSignalQueue()
	// Pre-fill one pending signal that DrainBurst will consume on its first poll.
	s.Flush()

	// Background producer flushes once mid-window; coalesced cap-1 means only
	// one extra signal is observable regardless of how many Flush() we issue.
	go func() {
		time.Sleep(2 * time.Millisecond)
		s.Flush()
		s.Flush()
		s.Flush()
	}()

	start := time.Now()
	s.DrainBurst(context.Background(), 10*time.Millisecond, 4)
	elapsed := time.Since(start)
	// We saw at most 2 signals (initial + one coalesced). Each consumed
	// signal triggers another debounce window of up to 10ms. Bound generously
	// to keep the test stable on slow CI hardware.
	assert.Less(t, elapsed, 100*time.Millisecond)
}

func TestSignalQueueZeroWake(t *testing.T) {
	s := NewSignalQueue()
	// Cap-1 semantics: many flushes preserve a single wake.
	for range 100 {
		s.Flush()
	}
	count := 0
loop:
	for {
		select {
		case <-s.C():
			count++
		default:
			break loop
		}
	}
	assert.Equal(t, 1, count)
}
