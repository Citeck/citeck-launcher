package namespace

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

func newTestDispatcher(t *testing.T) (*Dispatcher, *sync.WaitGroup, *SignalQueue, chan workers.Result) {
	t.Helper()
	wg := &sync.WaitGroup{}
	d := NewDispatcher(context.Background(), wg, 4)
	sig := NewSignalQueue()
	results := make(chan workers.Result, 32)
	return d, wg, sig, results
}

func TestDispatcherDispatchRuns(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	id := workers.TaskID{App: "x", Op: workers.OpStart}
	d.Dispatch(id, func(_ context.Context) workers.Result {
		return workers.Result{Payload: workers.StartPayload{ContainerID: "c1"}}
	}, results, sig)

	select {
	case res := <-results:
		assert.Equal(t, id, res.TaskID)
		assert.Equal(t, int64(1), res.AttemptID)
		require.NoError(t, res.Err)
		p, ok := res.Payload.(workers.StartPayload)
		require.True(t, ok)
		assert.Equal(t, "c1", p.ContainerID)
	case <-time.After(time.Second):
		t.Fatal("worker result not received")
	}
	// Signal flushed.
	select {
	case <-sig.C():
	default:
		t.Fatal("expected signal.Flush() after worker result")
	}
}

func TestDispatcherSupersedesOverlapping(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	id := workers.TaskID{App: "x", Op: workers.OpPull}
	startedFn1 := make(chan struct{})
	releaseFn1 := make(chan struct{})

	// First dispatch — blocks until releaseFn1 closed OR ctx is canceled.
	d.Dispatch(id, func(ctx context.Context) workers.Result {
		close(startedFn1)
		select {
		case <-ctx.Done():
			return workers.Result{Err: ctx.Err()}
		case <-releaseFn1:
			return workers.Result{}
		}
	}, results, sig)

	<-startedFn1

	// Second dispatch — same TaskID supersedes.
	d.Dispatch(id, func(_ context.Context) workers.Result {
		return workers.Result{Payload: workers.PullPayload{Digest: "sha256:second"}}
	}, results, sig)

	// Two results expected: the canceled fn1 and fn2.
	got := map[int64]workers.Result{}
	for i := range 2 {
		select {
		case res := <-results:
			got[res.AttemptID] = res
		case <-time.After(2 * time.Second):
			t.Fatalf("only got %d results", i)
		}
	}
	close(releaseFn1) // safety release in case fn1 was waiting on it after ctx-fast-path

	// Attempt 1 should be canceled; reason on the dispatcher slot would have
	// been Superseded. The slot may have been overwritten by attempt 2, but
	// the canceled fn1 result error indicates ctx cancellation.
	res1, ok := got[1]
	require.True(t, ok)
	require.Error(t, res1.Err)

	res2, ok := got[2]
	require.True(t, ok)
	require.NoError(t, res2.Err)
	p, _ := res2.Payload.(workers.PullPayload)
	assert.Equal(t, "sha256:second", p.Digest)
}

func TestDispatcherCancelApp(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	app := "alfresco"
	dispatchBlock := func(op workers.OpKind) chan struct{} {
		started := make(chan struct{})
		d.Dispatch(workers.TaskID{App: app, Op: op}, func(ctx context.Context) workers.Result {
			close(started)
			<-ctx.Done()
			return workers.Result{Err: ctx.Err()}
		}, results, sig)
		return started
	}

	pullStarted := dispatchBlock(workers.OpPull)
	startStarted := dispatchBlock(workers.OpStart)
	stopStarted := dispatchBlock(workers.OpStop)
	<-pullStarted
	<-startStarted
	<-stopStarted

	// Cancel everything for app EXCEPT OpStop.
	canceled := d.CancelApp(app, workers.CancelStopApp, workers.OpStop)
	assert.Equal(t, 2, canceled)

	// The pull and start should now return canceled errors; stop must remain
	// alive (we'll cancel it manually).
	got := map[workers.OpKind]workers.Result{}
	for i := range 2 {
		select {
		case res := <-results:
			got[res.TaskID.Op] = res
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d cancellations observed", i)
		}
	}
	_, hasPull := got[workers.OpPull]
	_, hasStart := got[workers.OpStart]
	assert.True(t, hasPull)
	assert.True(t, hasStart)

	// Verify the reason was recorded for one of the canceled tasks before
	// they were removed from the slot. (The slot may still be present until
	// applyWorkerResult calls ForgetTask.)
	// Now cancel stop for cleanup.
	assert.True(t, d.Cancel(workers.TaskID{App: app, Op: workers.OpStop}, workers.CancelDetach))
	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("stop did not exit after Cancel")
	}
}

func TestDispatcherCancelAll(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	for _, app := range []string{"a", "b", "c"} {
		started := make(chan struct{})
		d.Dispatch(workers.TaskID{App: app, Op: workers.OpPull}, func(ctx context.Context) workers.Result {
			close(started)
			<-ctx.Done()
			return workers.Result{Err: ctx.Err()}
		}, results, sig)
		<-started
	}

	canceled := d.CancelAll(workers.CancelDetach)
	assert.Equal(t, 3, canceled)

	for range 3 {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("CancelAll did not unblock all workers")
		}
	}
}

func TestDispatcherPullSemaphoreBounds(t *testing.T) {
	wg := &sync.WaitGroup{}
	d := NewDispatcher(context.Background(), wg, 2)
	sig := NewSignalQueue()
	results := make(chan workers.Result, 16)
	defer wg.Wait()

	const tasks = 6
	const block = 100 * time.Millisecond
	var inFlight atomic.Int32
	var maxObserved atomic.Int32

	start := time.Now()
	for i := range tasks {
		app := string(rune('a' + i))
		d.Dispatch(workers.TaskID{App: app, Op: workers.OpPull}, func(_ context.Context) workers.Result {
			cur := inFlight.Add(1)
			for {
				prev := maxObserved.Load()
				if cur <= prev || maxObserved.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(block)
			inFlight.Add(-1)
			return workers.Result{}
		}, results, sig)
	}

	for range tasks {
		<-results
	}
	elapsed := time.Since(start)

	assert.LessOrEqual(t, int(maxObserved.Load()), 2, "dispatcher's pullSem must bound concurrent pulls to 2")
	// 6 tasks / 2 slots = 3 batches; each blocks 100ms.
	assert.GreaterOrEqual(t, elapsed, 3*block-20*time.Millisecond, "expected serialization across pull batches")
}

func TestDispatcherStaleResultDropped(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	id := workers.TaskID{App: "x", Op: workers.OpStart}
	started := make(chan struct{})
	release := make(chan struct{})
	d.Dispatch(id, func(ctx context.Context) workers.Result {
		close(started)
		select {
		case <-release:
			return workers.Result{}
		case <-ctx.Done():
			return workers.Result{Err: ctx.Err()}
		}
	}, results, sig)
	<-started

	// Supersede.
	d.Dispatch(id, func(_ context.Context) workers.Result {
		return workers.Result{}
	}, results, sig)

	// Drain both results.
	got := []workers.Result{}
	for i := range 2 {
		select {
		case res := <-results:
			got = append(got, res)
		case <-time.After(time.Second):
			t.Fatalf("only got %d results", i)
		}
	}
	close(release)

	// Current should report attempt 2 (the latest).
	assert.Equal(t, int64(2), d.Current(id))
	// Find the stale (attempt 1) result; the caller — applyWorkerResult — is
	// the one that actually drops based on this discrepancy.
	for _, r := range got {
		if r.AttemptID == 1 {
			assert.NotEqual(t, d.Current(id), r.AttemptID, "stale attempt is not current")
		}
	}
}

func TestDispatcherCancelReasonLookup(t *testing.T) {
	d, wg, sig, results := newTestDispatcher(t)
	defer wg.Wait()

	id := workers.TaskID{App: "x", Op: workers.OpStart}
	started := make(chan struct{})
	d.Dispatch(id, func(ctx context.Context) workers.Result {
		close(started)
		<-ctx.Done()
		return workers.Result{Err: ctx.Err()}
	}, results, sig)
	<-started

	assert.True(t, d.Cancel(id, workers.CancelStopApp))
	assert.Equal(t, workers.CancelStopApp, d.CancelReason(id, 1))
	// Wrong attempt -> CancelNone.
	assert.Equal(t, workers.CancelNone, d.CancelReason(id, 99))

	<-results
}
