// Package downloadhttp bounds download inactivity without limiting transfer duration.
package downloadhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// IdleTimeout is the maximum wait for headers or another response byte.
const IdleTimeout = 30 * time.Second

// ErrIdleTimeout identifies a transfer that stopped receiving bytes.
var ErrIdleTimeout = errors.New("download timed out waiting for data")

// Do preserves the client's transport and redirect policy, but replaces its
// total request timeout with a byte inactivity deadline. Close the returned body.
// Parent context cancellation and deadlines remain authoritative.
func Do(client *http.Client, req *http.Request, idle time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	body := &idleBody{ctx: ctx, cancel: cancel, idle: idle, last: time.Now()}
	body.mu.Lock()
	body.timer = time.AfterFunc(idle, body.expire)
	body.mu.Unlock()
	downloadClient := *client
	downloadClient.Timeout = 0
	resp, err := downloadClient.Do(req.Clone(ctx)) //nolint:gosec // G704: caller validates snapshot URLs or constructs release URLs; this helper preserves that policy.
	if err != nil {
		body.stop()
		cause := context.Cause(ctx)
		cancel(context.Canceled)
		if cause != nil {
			return nil, fmt.Errorf("download request: %w", cause)
		}
		return nil, fmt.Errorf("download request: %w", err)
	}
	body.body = resp.Body
	body.touch() // Headers arrived; start waiting for the first body byte.
	resp.Body = body
	return resp, nil
}

type idleBody struct {
	body    io.ReadCloser
	ctx     context.Context
	cancel  context.CancelCauseFunc
	mu      sync.Mutex
	timer   *time.Timer
	idle    time.Duration
	last    time.Time
	stopped bool
}

func (b *idleBody) expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	if remaining := b.idle - time.Since(b.last); remaining > 0 {
		b.timer.Reset(remaining)
		return
	}
	b.cancel(ErrIdleTimeout)
}

func (b *idleBody) touch() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.stopped {
		b.last = time.Now()
		b.timer.Reset(b.idle)
	}
}

func (b *idleBody) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	b.timer.Stop()
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.touch()
	}
	if err != nil {
		b.stop()
		if cause := context.Cause(b.ctx); cause != nil {
			return n, fmt.Errorf("read download: %w", cause)
		}
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("read download: %w", err)
	}
	return n, nil
}

func (b *idleBody) Close() error {
	b.stop()
	b.cancel(context.Canceled)
	if err := b.body.Close(); err != nil {
		return fmt.Errorf("close download: %w", err)
	}
	return nil
}
