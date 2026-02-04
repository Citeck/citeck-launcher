// Package actions provides a universal action execution framework with
// retry support, worker pool, and stalled action detection.
package actions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ActionExecutor defines how a specific action type is executed.
type ActionExecutor interface {
	// Execute runs the action. Return nil for success, error to trigger retry.
	Execute(ctx context.Context, actx *ActionContext) error
	// Name returns a human-readable name for logging.
	Name(actx *ActionContext) string
	// RetryDelay returns the delay before the next retry attempt.
	// Return a negative duration to stop retrying.
	RetryDelay(actx *ActionContext) time.Duration
}

// ActionContext holds per-action state passed to the executor.
type ActionContext struct {
	// ID uniquely identifies this action execution.
	ID string
	// Attempt is the current attempt number (0-based).
	Attempt int
	// Data carries executor-specific payload.
	Data any
	// startedAtNs is when the current attempt started (UnixNano). Atomic for thread safety.
	startedAtNs atomic.Int64
}

// Heartbeat resets the stall timer, signaling that the action is still making progress.
// Call this from long-running executors (e.g. image pull with progress) to prevent
// stall detection from canceling the action.
func (actx *ActionContext) Heartbeat() {
	actx.startedAtNs.Store(time.Now().UnixNano())
}

// ActionStatus represents the lifecycle state of an action.
type ActionStatus int32

// Action lifecycle states.
const (
	StatusPending  ActionStatus = iota
	StatusRunning
	StatusDone
	StatusFailed
	StatusCanceled
	StatusStalled
)

func (s ActionStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusRunning:
		return "RUNNING"
	case StatusDone:
		return "DONE"
	case StatusFailed:
		return "FAILED"
	case StatusCanceled:
		return "CANCELED"
	case StatusStalled:
		return "STALLED"
	default:
		return "UNKNOWN"
	}
}

// ActionHandle is returned when an action is submitted. It can be used to
// wait for completion or cancel the action.
type ActionHandle struct {
	ID       string
	status   atomic.Int32
	err      error
	errMu    sync.Mutex
	done     chan struct{}
	cancelFn context.CancelFunc
}

// Status returns the current action status.
func (h *ActionHandle) Status() ActionStatus {
	return ActionStatus(h.status.Load())
}

// Err returns the error if the action failed.
func (h *ActionHandle) Err() error {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.err
}

// Wait blocks until the action completes or ctx expires.
func (h *ActionHandle) Wait(ctx context.Context) error {
	select {
	case <-h.done:
		return h.Err()
	case <-ctx.Done():
		return fmt.Errorf("wait: %w", ctx.Err())
	}
}

// Cancel requests cancellation of this action.
func (h *ActionHandle) Cancel() {
	h.cancelFn()
}

func (h *ActionHandle) setStatus(s ActionStatus) {
	h.status.Store(int32(s))
}

func (h *ActionHandle) setErr(err error) {
	h.errMu.Lock()
	h.err = err
	h.errMu.Unlock()
}

// ActionParams configures a new action submission.
type ActionParams struct {
	Executor ActionExecutor
	Data     any
}

// ServiceConfig controls the action service behavior.
type ServiceConfig struct {
	// WorkerCount is the number of concurrent worker goroutines. Default: 20.
	WorkerCount int
	// StalledTimeout is how long an action can run before being marked stalled. Default: 120s.
	StalledTimeout time.Duration
	// WatchInterval is how often the stalled watcher checks. Default: 10s.
	WatchInterval time.Duration
}

func (c ServiceConfig) withDefaults() ServiceConfig {
	if c.WorkerCount <= 0 {
		c.WorkerCount = 20
	}
	if c.StalledTimeout <= 0 {
		c.StalledTimeout = 120 * time.Second
	}
	if c.WatchInterval <= 0 {
		c.WatchInterval = 10 * time.Second
	}
	return c
}

// Service manages action execution with a worker pool and retry scheduling.
type Service struct {
	cfg    ServiceConfig
	svcCtx context.Context
	queue  chan *actionEntry
	cancel context.CancelFunc
	wg     sync.WaitGroup
	nextID atomic.Int64
	active sync.Map // id -> *actionEntry
}

type actionEntry struct {
	handle   *ActionHandle
	executor ActionExecutor
	actx     *ActionContext
	ctx      context.Context
}

// NewService creates and starts a new action service.
func NewService(cfg ServiceConfig) *Service {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())

	s := &Service{
		cfg:    cfg,
		svcCtx: ctx,
		queue:  make(chan *actionEntry, 256),
		cancel: cancel,
	}

	// Start worker pool
	for i := 0; i < cfg.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	// Start stalled action watcher
	s.wg.Add(1)
	go s.watcher(ctx)

	return s
}

// Execute submits an action for execution and returns a handle.
func (s *Service) Execute(params ActionParams) *ActionHandle {
	id := fmt.Sprintf("action-%d", s.nextID.Add(1))
	ctx, cancelFn := context.WithCancel(s.svcCtx)

	handle := &ActionHandle{
		ID:       id,
		done:     make(chan struct{}),
		cancelFn: cancelFn,
	}
	handle.setStatus(StatusPending)

	actx := &ActionContext{
		ID:   id,
		Data: params.Data,
	}

	entry := &actionEntry{
		handle:   handle,
		executor: params.Executor,
		actx:     actx,
		ctx:      ctx,
	}

	s.active.Store(id, entry)

	select {
	case s.queue <- entry:
	default:
		slog.Warn("Action queue full, running inline", "id", id)
		s.wg.Go(func() {
			s.runAction(entry)
		})
	}

	return handle
}

// Shutdown stops the service and waits for all workers to finish.
// Cancels the service context which propagates to all active actions.
func (s *Service) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// ActiveCount returns the number of currently active (pending + running) actions.
func (s *Service) ActiveCount() int {
	count := 0
	s.active.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (s *Service) worker(svcCtx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-svcCtx.Done():
			return
		case entry := <-s.queue:
			if entry == nil {
				return
			}
			s.runAction(entry)
		}
	}
}

func (s *Service) watcher(svcCtx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.WatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-svcCtx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.active.Range(func(_, value any) bool {
				entry := value.(*actionEntry)
				handle := entry.handle
				ns := entry.actx.startedAtNs.Load()
				if handle.Status() == StatusRunning && ns != 0 {
					elapsed := now.Sub(time.Unix(0, ns))
					if elapsed > s.cfg.StalledTimeout {
						name := entry.executor.Name(entry.actx)
						slog.Warn("Action stalled", "id", entry.actx.ID, "name", name, "elapsed", elapsed)
						handle.setStatus(StatusStalled)
						handle.setErr(fmt.Errorf("action stalled after %s", elapsed))
						handle.cancelFn()
					}
				}
				return true
			})
		}
	}
}

func (s *Service) runAction(entry *actionEntry) {
	handle := entry.handle
	actx := entry.actx
	executor := entry.executor
	ctx := entry.ctx

	defer func() {
		s.active.Delete(actx.ID)
		close(handle.done)
	}()

	for {
		// Check action-level cancellation
		select {
		case <-ctx.Done():
			s.finishCanceled(handle)
			return
		default:
		}

		handle.setStatus(StatusRunning)
		actx.startedAtNs.Store(time.Now().UnixNano())

		name := executor.Name(actx)
		slog.Debug("Executing action", "id", actx.ID, "name", name, "attempt", actx.Attempt)

		err := executor.Execute(ctx, actx)
		if err == nil {
			handle.setStatus(StatusDone)
			slog.Debug("Action completed", "id", actx.ID, "name", name)
			return
		}

		slog.Warn("Action failed", "id", actx.ID, "name", name, "attempt", actx.Attempt, "err", err)

		// If context was canceled (by Cancel() or stalled watcher), don't retry
		if ctx.Err() != nil {
			s.finishCanceled(handle)
			return
		}

		// Check retry
		delay := executor.RetryDelay(actx)
		if delay < 0 {
			handle.setErr(err)
			handle.setStatus(StatusFailed)
			return
		}

		actx.Attempt++

		select {
		case <-ctx.Done():
			s.finishCanceled(handle)
			return
		case <-time.After(delay):
			// continue to next attempt
		}
	}
}

// finishCanceled sets the final status for a canceled action.
// If the watcher already marked it Stalled, that status is preserved.
func (s *Service) finishCanceled(handle *ActionHandle) {
	if handle.Status() != StatusStalled {
		handle.setStatus(StatusCanceled)
	}
}
