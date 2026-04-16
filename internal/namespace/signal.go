// Package namespace — signal queue primitive.
//
// SignalQueue is a 1-element channel used by workers / state-mutators to wake the
// runtimeLoop without blocking. Multiple Flush() calls coalesce into at most one
// pending wake (cap-1 semantics). DrainBurst collapses a short burst of follow-up
// signals into the same wake so the loop performs one full step instead of N.
//
// Construct with NewSignalQueue. The zero value is intentionally unusable — Flush
// and C() will panic on a nil channel; tests / production code MUST use the
// constructor.
package namespace

import (
	"context"
	"time"
)

// SignalQueue coalesces wake-up signals via a cap-1 buffered channel.
type SignalQueue struct {
	ch chan struct{}
}

// NewSignalQueue returns a ready-to-use SignalQueue.
func NewSignalQueue() *SignalQueue {
	return &SignalQueue{ch: make(chan struct{}, 1)}
}

// Flush attempts a non-blocking send. If a signal is already pending, this is a
// no-op (coalescing). Never blocks.
func (s *SignalQueue) Flush() {
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

// C returns the receive end for use in select.
func (s *SignalQueue) C() <-chan struct{} {
	return s.ch
}

// DrainBurst polls up to maxPolls additional times waiting up to debounce per
// poll, after the first wake has already been consumed by the caller. Exits
// on the first timeout, on ctx.Done(), or after consuming maxPolls extra
// signals.
//
// The intent is to collapse rapid bursts of Flush() calls into a single wake;
// it does NOT do work, only consumes pending signals so the next outer-loop
// iteration sees a quiet queue.
func (s *SignalQueue) DrainBurst(ctx context.Context, debounce time.Duration, maxPolls int) {
	if maxPolls <= 0 {
		return
	}
	timer := time.NewTimer(debounce)
	defer timer.Stop()
	for i := range maxPolls {
		// Reset the timer for each poll so the budget is per-iteration.
		if i > 0 {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.ch:
			// Consumed one extra signal; loop again.
		case <-timer.C:
			return
		}
	}
}
