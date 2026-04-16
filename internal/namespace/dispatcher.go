// Package namespace — per-Runtime worker dispatcher.
//
// Dispatcher owns the per-(app, opKind) task table and worker goroutine
// launching. Every Dispatch call supersedes any prior in-flight task with the
// same TaskID (canceling its context with reason=Superseded), bumps an
// attemptID counter, and stamps (TaskID, AttemptID) onto the worker's Result
// before publishing.
//
// The task table is logically owned by runtimeLoop in production paths.
// The internal Mutex is a belt-and-suspenders guard that lets external API
// goroutines (StopApp, etc.) and tests Dispatch / Cancel safely.
//
// The pull semaphore (pullSem) bounds concurrent OpPull workers. Callers
// should only call SetPullConcurrency during initialization (replacing pullSem
// mid-flight does not cancel pulls already blocked on the old channel; the old
// channel is left alive so they finish, but those slots are no longer counted
// against the new bound).
package namespace

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/namespace/workers"
)

// taskState holds per-slot bookkeeping for the dispatcher table.
type taskState struct {
	attemptID    int64
	cancel       context.CancelFunc
	cancelReason workers.CancelReason
}

// signaler is the minimal Flush() surface Dispatcher needs. SignalQueue
// implements it.
type signaler interface {
	Flush()
}

// Dispatcher launches worker tasks and tracks supersession + cancellation.
type Dispatcher struct {
	mu          sync.Mutex
	tasks       map[workers.TaskID]*taskState
	nextAttempt map[workers.TaskID]int64
	pullSem     chan struct{}
	parentCtx   context.Context
	workerWg    *sync.WaitGroup

	// activeWorkers counts worker goroutines currently executing fn(ctx).
	// Incremented in Dispatch, decremented when the func returns. Decoupled
	// from len(tasks) — the supersession table is cleaned up by runtimeLoop
	// via ForgetTask. doDetach polls ActiveWorkers() to wait for worker drain
	// without deadlocking on r.wg (which also tracks runtimeLoop).
	activeWorkers atomic.Int64
}

// defaultPullConcurrency mirrors the legacy r.pullSem cap.
const defaultPullConcurrency = 4

// NewDispatcher constructs a Dispatcher. parentCtx is the root context for all
// worker contexts; canceling it cancels every in-flight worker. wg tracks
// running worker goroutines so callers can Wait on shutdown.
func NewDispatcher(parentCtx context.Context, wg *sync.WaitGroup, pullConcurrency int) *Dispatcher {
	if pullConcurrency <= 0 {
		pullConcurrency = defaultPullConcurrency
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if wg == nil {
		wg = &sync.WaitGroup{}
	}
	return &Dispatcher{
		tasks:       make(map[workers.TaskID]*taskState),
		nextAttempt: make(map[workers.TaskID]int64),
		pullSem:     make(chan struct{}, pullConcurrency),
		parentCtx:   parentCtx,
		workerWg:    wg,
	}
}

// Dispatch supersedes any existing task with id, spawns a new worker
// goroutine, and posts the stamped Result to resultCh once fn returns. After
// publishing, signal.Flush() is called so runtimeLoop wakes.
//
// If id.Op == OpPull, the worker first acquires from pullSem (blocking, but
// honoring its own ctx so a Cancel mid-wait still aborts).
func (d *Dispatcher) Dispatch(
	id workers.TaskID,
	fn workers.TaskFunc,
	resultCh chan<- workers.Result,
	signal signaler,
) {
	d.mu.Lock()
	if existing, ok := d.tasks[id]; ok {
		existing.cancelReason = workers.CancelSuperseded
		existing.cancel()
	}
	d.nextAttempt[id]++
	attempt := d.nextAttempt[id]
	// cancel is stored on the task slot and invoked by Cancel / CancelApp /
	// CancelAll / supersession; not a leak. gosec's static analysis can't
	// see the deferred invocation through the map indirection.
	ctx, cancel := context.WithCancel(d.parentCtx) //nolint:gosec // cancel stored in taskState; called by Cancel/CancelAll
	d.tasks[id] = &taskState{attemptID: attempt, cancel: cancel}
	d.mu.Unlock()

	d.activeWorkers.Add(1)
	d.workerWg.Go(func() {
		defer d.activeWorkers.Add(-1)
		// Pull throttling honors ctx so Cancel during a wait aborts cleanly.
		if id.Op == workers.OpPull {
			select {
			case d.pullSem <- struct{}{}:
				defer func() { <-d.pullSem }()
			case <-ctx.Done():
				res := workers.Result{TaskID: id, AttemptID: attempt, Err: ctx.Err()}
				// Select on parentCtx.Done() while sending: if the parent
				// context has been canceled (runtime shutdown), runtimeLoop
				// may already have stopped draining resultCh. Drop the Result
				// in that case — runtimeLoop owns cleanup via d.workerWg on
				// shutdown. Per-task cancellation (Cancel / CancelApp /
				// supersession) uses the task-local ctx only; the Result must
				// still be delivered so the consumer can observe the
				// cancellation reason.
				d.sendResult(resultCh, res, signal)
				return
			}
		}
		res := fn(ctx)
		res.TaskID = id
		res.AttemptID = attempt
		// Same rationale as above: on parentCtx.Done() we drop; otherwise the
		// send completes normally.
		d.sendResult(resultCh, res, signal)
	})
}

// sendResult publishes res to resultCh unless the dispatcher's parentCtx has
// been canceled (runtime shutdown), in which case the Result is dropped to
// avoid blocking on a non-draining consumer. On successful send it fires
// signal.Flush() so runtimeLoop wakes. Shutdown cleanup is the caller's
// responsibility (runtimeLoop waits on d.workerWg).
func (d *Dispatcher) sendResult(resultCh chan<- workers.Result, res workers.Result, signal signaler) {
	select {
	case resultCh <- res:
		if signal != nil {
			signal.Flush()
		}
	case <-d.parentCtx.Done():
	}
}

// Cancel cancels the active task at id (if any) and records reason. Returns
// true if a task was active.
func (d *Dispatcher) Cancel(id workers.TaskID, reason workers.CancelReason) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.tasks[id]
	if !ok {
		return false
	}
	st.cancelReason = reason
	st.cancel()
	return true
}

// CancelApp cancels every active task for the named app except those whose Op
// appears in exceptOps. Returns the number of tasks canceled.
func (d *Dispatcher) CancelApp(app string, reason workers.CancelReason, exceptOps ...workers.OpKind) int {
	skip := make(map[workers.OpKind]struct{}, len(exceptOps))
	for _, op := range exceptOps {
		skip[op] = struct{}{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for id, st := range d.tasks {
		if id.App != app {
			continue
		}
		if _, ok := skip[id.Op]; ok {
			continue
		}
		st.cancelReason = reason
		st.cancel()
		count++
	}
	return count
}

// CancelAll cancels every active task. Returns the count.
func (d *Dispatcher) CancelAll(reason workers.CancelReason) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, st := range d.tasks {
		st.cancelReason = reason
		st.cancel()
		count++
	}
	return count
}

// Current returns the latest attemptID for id, or 0 if none.
func (d *Dispatcher) Current(id workers.TaskID) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if st, ok := d.tasks[id]; ok {
		return st.attemptID
	}
	return 0
}

// CancelReason returns the cancellation reason for (id, attemptID), or
// CancelNone if the slot is empty or attemptID does not match.
func (d *Dispatcher) CancelReason(id workers.TaskID, attemptID int64) workers.CancelReason {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.tasks[id]
	if !ok || st.attemptID != attemptID {
		return workers.CancelNone
	}
	return st.cancelReason
}

// ForgetTask removes the table entry for (id, attemptID). Safe no-op if
// attemptID is stale (a newer attempt has already replaced this slot).
// applyWorkerResult calls this AFTER processing a Result.
func (d *Dispatcher) ForgetTask(id workers.TaskID, attemptID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.tasks[id]
	if !ok || st.attemptID != attemptID {
		return
	}
	delete(d.tasks, id)
}

// ActiveWorkers returns the current number of worker goroutines executing
// (from Dispatch until fn returns). Used by doDetach to poll for worker drain
// without waiting on r.wg — which also tracks runtimeLoop and would deadlock.
//
// Decoupled from len(d.tasks): the tasks map is a supersession table cleaned
// up asynchronously by runtimeLoop via ForgetTask.
func (d *Dispatcher) ActiveWorkers() int64 {
	return d.activeWorkers.Load()
}

// SetPullConcurrency replaces the pull semaphore with one of the given size.
// Callers should only invoke this during initialization — replacing mid-flight
// leaves goroutines waiting on the old channel until they unblock naturally.
func (d *Dispatcher) SetPullConcurrency(n int) {
	if n <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pullSem = make(chan struct{}, n)
}
