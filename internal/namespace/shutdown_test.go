package namespace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignalShutdownIdempotent(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := NewRuntime(cfg, newMockDocker(), t.TempDir())
	defer close(r.eventCh)

	// Two calls must not panic.
	assert.NotPanics(t, func() {
		r.signalShutdown()
		r.signalShutdown()
	})
	// Channel is observably closed after both calls.
	select {
	case <-r.shutdownComplete:
		// ok — closed
	default:
		t.Fatal("shutdownComplete not closed after signalShutdown")
	}
	// Second observation must also see the close (channel close is permanent).
	select {
	case <-r.shutdownComplete:
		// ok — closed
	default:
		t.Fatal("shutdownComplete not closed on second observation")
	}
}

// TestSignalShutdownDoesNotBlockTeardown is the regression test for the
// shared-sync.Once bug where signalShutdown and shutdownAfter shared one Once.
// A cmdStop continuation that fired signalShutdown first would consume the
// guard and skip the entire teardown body on a later Shutdown() — leaking
// dispatchLoop and hanging wg.Wait in the daemon.
//
// teardownOnce + signalOnce are separate guards: signalShutdown only closes
// shutdownComplete; the full teardown body still runs on Shutdown().
func TestSignalShutdownDoesNotBlockTeardown(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := NewRuntime(cfg, newMockDocker(), t.TempDir())

	// Simulate the sequence where a stop continuation closes shutdownComplete.
	r.signalShutdown()

	// Verify the channel is observably closed (signalOnce did its job).
	select {
	case <-r.shutdownComplete:
		// ok
	default:
		t.Fatal("shutdownComplete not closed after signalShutdown")
	}

	// Now run the full teardown. Must NOT short-circuit on the consumed
	// signalOnce — teardownOnce is a separate guard.
	done := make(chan struct{})
	go func() {
		r.Shutdown()
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown() did not return within 5s — teardown was skipped")
	}

	// Concrete proof the teardown body ran: eventCh is closed, dispatchLoop
	// exited. A receive on a closed channel returns zero-value, ok=false.
	select {
	case _, ok := <-r.eventCh:
		assert.False(t, ok, "eventCh must be closed after Shutdown — teardown body did not run")
	case <-time.After(time.Second):
		t.Fatal("eventCh receive blocked — channel not closed; teardown body did not run")
	}
}
