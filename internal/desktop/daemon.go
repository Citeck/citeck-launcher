package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/daemon"
)

// DaemonOpts configures the daemon restart loop.
type DaemonOpts struct {
	Version string
	ReadyCh chan<- string // notified once when daemon HTTP server is ready; nil = ignored
	Status  *DaemonStatus // observable status for UI error display; nil = ignored
}

// DaemonStatus holds the current daemon state, observable by the UI layer.
type DaemonStatus struct {
	lastError atomic.Value // stores string
	failures  atomic.Int32
	ready     atomic.Bool
}

// SetError records the last daemon error.
func (s *DaemonStatus) SetError(err error) {
	if err != nil {
		s.lastError.Store(err.Error())
	} else {
		s.lastError.Store("")
	}
}

// LastError returns the last daemon error message, or empty string.
func (s *DaemonStatus) LastError() string {
	v, _ := s.lastError.Load().(string)
	return v
}

// Failures returns the current consecutive failure count.
func (s *DaemonStatus) Failures() int {
	return int(s.failures.Load())
}

// IsReady returns true if the daemon has started successfully at least once.
func (s *DaemonStatus) IsReady() bool {
	return s.ready.Load()
}

// RunDaemonLoop runs the daemon with automatic restart on crash.
// It blocks until ctx is canceled or max consecutive failures is reached.
func RunDaemonLoop(ctx context.Context, opts DaemonOpts) error {
	backoff := 5 * time.Second
	maxBackoff := 60 * time.Second
	failures := 0
	maxFailures := 20

	// readyOnce ensures opts.ReadyCh is sent to exactly once.
	var readyOnce sync.Once

	for {
		if failures >= maxFailures {
			msg := fmt.Sprintf("daemon failed %d times consecutively, waiting for restart", failures)
			slog.Error(msg)
			if opts.Status != nil {
				opts.Status.SetError(fmt.Errorf("%s", msg))
			}
			<-ctx.Done()
			return nil
		}

		if opts.Status != nil && failures > 0 {
			opts.Status.failures.Store(int32(failures))
		}

		startedAt := time.Now()

		// Per-iteration channel: daemon.Start sends to it while still blocking in Serve().
		var ch chan string
		if opts.ReadyCh != nil {
			ch = make(chan string, 1)
			go func() {
				url, ok := <-ch
				if ok {
					readyOnce.Do(func() { opts.ReadyCh <- url })
					if opts.Status != nil {
						opts.Status.ready.Store(true)
						opts.Status.SetError(nil)
						opts.Status.failures.Store(0)
					}
				} else {
					readyOnce.Do(func() { opts.ReadyCh <- "" })
				}
			}()
		}

		err := daemon.Start(daemon.StartOptions{
			Ctx:     ctx,
			Desktop: true,
			Version: opts.Version,
			ReadyCh: ch,
		})

		// Close ch to unblock the goroutine if daemon.Start returned without sending
		if ch != nil {
			close(ch)
		}

		if ctx.Err() != nil || errors.Is(err, daemon.ErrShutdownRequested) {
			return nil
		}

		// Daemon ran >30s = real work done, reset backoff
		if time.Since(startedAt) > 30*time.Second {
			backoff = 5 * time.Second
			failures = 0
		} else {
			failures++
		}

		slog.Error("Daemon exited unexpectedly", "err", err, "retry", backoff, "failures", failures)
		if opts.Status != nil {
			opts.Status.SetError(err)
		}

		select {
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		case <-ctx.Done():
			return nil
		}
	}
}
