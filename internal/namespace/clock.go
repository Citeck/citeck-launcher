// Package namespace — clock abstraction for testable time.
//
// Production code uses realClock (returns time.Now()). Tests substitute
// FakeClock via newRuntimeForTest's WithTestClock option to drive deterministic
// timeouts (group-stop, retry backoff, liveness scheduling) without sleeping.
package namespace

import (
	"sync"
	"time"
)

// Clock abstracts the wall clock for testability.
type Clock interface {
	Now() time.Time
}

// realClock is the production implementation.
type realClock struct{}

// Now returns the current wall time.
func (realClock) Now() time.Time { return time.Now() }

// FakeClock is a goroutine-safe fake clock for tests.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFakeClock constructs a FakeClock anchored at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{t: start}
}

// Now returns the current fake time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Set replaces the current time with t.
func (f *FakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}
