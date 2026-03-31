package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunDaemonLoop_CleanQuitOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- RunDaemonLoop(ctx, DaemonOpts{Version: "test"})
	}()

	// Give the loop time to enter daemon.Start (may block on git operations)
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("RunDaemonLoop did not exit after context cancel")
	}
}

func TestRunDaemonLoop_ReadyChNotifiedOnFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	readyCh := make(chan string, 1)

	go RunDaemonLoop(ctx, DaemonOpts{
		Version: "test",
		ReadyCh: readyCh,
	})

	// daemon.Start will either succeed or fail in test env —
	// either way readyCh must be notified within the first iteration
	select {
	case <-readyCh:
		// ok — got notification
	case <-time.After(30 * time.Second):
		t.Fatal("readyCh was not notified after first daemon.Start attempt")
	}
}

func TestRunDaemonLoop_ReadyChNotifiedOnlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Unbuffered — a second send would block and never be received by default case
	readyCh := make(chan string)

	go RunDaemonLoop(ctx, DaemonOpts{
		Version: "test",
		ReadyCh: readyCh,
	})

	// Drain first notification
	select {
	case <-readyCh:
	case <-time.After(30 * time.Second):
		t.Fatal("first readyCh notification not received")
	}

	// Wait long enough for 2 restart cycles (>5s backoff + daemon.Start time).
	// Use generous margin to avoid CI flakiness.
	time.Sleep(12 * time.Second)

	select {
	case <-readyCh:
		t.Fatal("readyCh received a second notification — should only fire once")
	default:
		// Good — no second send
	}
}
