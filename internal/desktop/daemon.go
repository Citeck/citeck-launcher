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
	logBuf    LogBuffer
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

// LogLines returns the last N startup log lines (captured before daemon was ready).
func (s *DaemonStatus) LogLines() string {
	return s.logBuf.String()
}

// LogWriter returns the io.Writer for capturing daemon startup logs.
func (s *DaemonStatus) LogWriter() *LogBuffer {
	return &s.logBuf
}

// LogBuffer is a thread-safe ring buffer that captures recent log output.
type LogBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

const maxLogBufSize = 64 * 1024 // 64KB — enough for startup logs

// Write implements io.Writer. Keeps only the last maxLogBufSize bytes.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxLogBufSize {
		b.buf = b.buf[len(b.buf)-maxLogBufSize:]
	}
	b.size += len(p)
	return len(p), nil
}

// String returns the buffered content.
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Clear resets the buffer.
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = b.buf[:0]
	b.size = 0
}

// RunDaemonLoop runs the daemon with automatic restart on crash.
// It blocks until ctx is canceled or max consecutive failures is reached.
func RunDaemonLoop(ctx context.Context, opts DaemonOpts) error {
	backoff := 5 * time.Second
	maxBackoff := 60 * time.Second
	failures := 0
	maxFailures := 20

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
						opts.Status.SetError(nil)
						opts.Status.failures.Store(0)
						opts.Status.logBuf.Clear() // success — clear startup logs
					}
				} else {
					readyOnce.Do(func() { opts.ReadyCh <- "" })
				}
			}()
		}

		// Pass log writer so daemon startup logs are captured
		var startOpts daemon.StartOptions
		startOpts.Ctx = ctx
		startOpts.Desktop = true
		startOpts.Version = opts.Version
		startOpts.ReadyCh = ch
		if opts.Status != nil {
			startOpts.LogWriter = opts.Status.LogWriter()
		}

		err := daemon.Start(startOpts)

		if ch != nil {
			close(ch)
		}

		if ctx.Err() != nil || errors.Is(err, daemon.ErrShutdownRequested) {
			return nil
		}

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
