package desktop

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/daemon"
)

// DaemonOpts configures the daemon restart loop.
type DaemonOpts struct {
	Version string
	ReadyCh chan<- string // notified once when daemon socket is listening; nil = ignored
	NoUI    bool         // disable TCP listener (desktop proxies via socket)
}

// RunDaemonLoop runs the daemon with automatic restart on crash.
// It blocks until ctx is cancelled or max consecutive failures is reached.
func RunDaemonLoop(ctx context.Context, opts DaemonOpts) error {
	backoff := 5 * time.Second
	maxBackoff := 60 * time.Second
	failures := 0
	maxFailures := 20

	// readyOnce ensures opts.ReadyCh is sent to exactly once.
	var readyOnce sync.Once

	for {
		if failures >= maxFailures {
			slog.Error("Max daemon failures reached, waiting for quit", "failures", failures)
			<-ctx.Done()
			return nil
		}

		startedAt := time.Now()

		// Per-iteration channel: daemon.Start sends to it while still blocking in Serve().
		// A goroutine forwards to opts.ReadyCh (at most once via readyOnce).
		// Closing ch after daemon.Start returns unblocks the goroutine on failure — no leak.
		var ch chan string
		if opts.ReadyCh != nil {
			ch = make(chan string, 1)
			go func() {
				url, ok := <-ch
				if ok {
					readyOnce.Do(func() { opts.ReadyCh <- url })
				} else {
					readyOnce.Do(func() { opts.ReadyCh <- "" })
				}
			}()
		}

		err := daemon.Start(daemon.StartOptions{
			Ctx:     ctx,
			Desktop: true,
			NoUI:    opts.NoUI,
			Version: opts.Version,
			ReadyCh: ch,
		})

		// Close ch to unblock the goroutine if daemon.Start returned without sending
		// (or already sent — close after read is harmless for buffered channels).
		if ch != nil {
			close(ch)
		}

		if ctx.Err() != nil || errors.Is(err, daemon.ErrShutdownRequested) {
			return nil // clean quit
		}

		// Daemon ran >30s = real work done, reset backoff
		if time.Since(startedAt) > 30*time.Second {
			backoff = 5 * time.Second
			failures = 0
		} else {
			failures++
		}

		slog.Error("Daemon exited unexpectedly", "err", err, "retry", backoff, "failures", failures)

		select {
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		case <-ctx.Done():
			return nil
		}
	}
}
