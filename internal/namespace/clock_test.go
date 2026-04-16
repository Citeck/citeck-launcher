package namespace

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClockReturnsNow(t *testing.T) {
	c := realClock{}
	a := c.Now()
	b := time.Now()
	assert.WithinDuration(t, b, a, 50*time.Millisecond)
}

func TestFakeClockAdvance(t *testing.T) {
	start := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	f := NewFakeClock(start)
	assert.Equal(t, start, f.Now())
	f.Advance(2 * time.Hour)
	assert.Equal(t, start.Add(2*time.Hour), f.Now())
}

func TestFakeClockSet(t *testing.T) {
	f := NewFakeClock(time.Unix(0, 0))
	target := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Set(target)
	assert.Equal(t, target, f.Now())
}

func TestFakeClockConcurrent(t *testing.T) {
	start := time.Now()
	f := NewFakeClock(start)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 100 {
				f.Advance(time.Millisecond)
				_ = f.Now()
			}
		})
	}
	wg.Wait()
	// 10 goroutines * 100 iterations = 1000ms total advance.
	assert.Equal(t, start.Add(1000*time.Millisecond), f.Now())
}
