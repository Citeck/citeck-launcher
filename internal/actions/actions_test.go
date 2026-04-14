package actions

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// --- Test executors ---

// successExecutor always succeeds on the first attempt.
type successExecutor struct{}

func (e *successExecutor) Execute(_ context.Context, _ *ActionContext) error { return nil }
func (e *successExecutor) Name(_ *ActionContext) string                      { return "success" }
func (e *successExecutor) RetryDelay(_ *ActionContext) time.Duration         { return -1 }

// failExecutor always fails and does not retry.
type failExecutor struct{}

func (e *failExecutor) Execute(_ context.Context, _ *ActionContext) error {
	return errors.New("always fails")
}
func (e *failExecutor) Name(_ *ActionContext) string              { return "fail" }
func (e *failExecutor) RetryDelay(_ *ActionContext) time.Duration { return -1 }

// retryExecutor fails N times then succeeds.
type retryExecutor struct {
	failCount int
	calls     atomic.Int32
}

func (e *retryExecutor) Execute(_ context.Context, actx *ActionContext) error {
	n := int(e.calls.Add(1))
	if n <= e.failCount {
		return errors.New("transient error")
	}
	return nil
}

func (e *retryExecutor) Name(_ *ActionContext) string { return "retry" }

func (e *retryExecutor) RetryDelay(actx *ActionContext) time.Duration {
	if actx.Attempt >= e.failCount {
		return -1 // stop retrying
	}
	return 10 * time.Millisecond
}

// slowExecutor takes a long time to run (for stalled detection testing).
type slowExecutor struct {
	duration time.Duration
}

func (e *slowExecutor) Execute(ctx context.Context, _ *ActionContext) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("slow executor: %w", ctx.Err())
	case <-time.After(e.duration):
		return nil
	}
}

func (e *slowExecutor) Name(_ *ActionContext) string              { return "slow" }
func (e *slowExecutor) RetryDelay(_ *ActionContext) time.Duration { return -1 }

// counterExecutor counts how many times Execute is called.
type counterExecutor struct {
	count atomic.Int32
}

func (e *counterExecutor) Execute(_ context.Context, _ *ActionContext) error {
	e.count.Add(1)
	return nil
}

func (e *counterExecutor) Name(_ *ActionContext) string              { return "counter" }
func (e *counterExecutor) RetryDelay(_ *ActionContext) time.Duration { return -1 }

// --- Helper ---

func newTestService(opts ...func(*ServiceConfig)) *Service {
	cfg := ServiceConfig{
		WorkerCount:    4,
		StalledTimeout: 200 * time.Millisecond,
		WatchInterval:  50 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewService(cfg)
}

// --- Tests ---

func TestExecute_Success(t *testing.T) {
	svc := newTestService()
	defer svc.Shutdown()

	h := svc.Execute(ActionParams{Executor: &successExecutor{}})
	err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if h.Status() != StatusDone {
		t.Errorf("expected StatusDone, got %s", h.Status())
	}
}

func TestExecute_Failure(t *testing.T) {
	svc := newTestService()
	defer svc.Shutdown()

	h := svc.Execute(ActionParams{Executor: &failExecutor{}})
	err := h.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if h.Status() != StatusFailed {
		t.Errorf("expected StatusFailed, got %s", h.Status())
	}
}

func TestExecute_RetryThenSucceed(t *testing.T) {
	svc := newTestService()
	defer svc.Shutdown()

	exec := &retryExecutor{failCount: 3}
	h := svc.Execute(ActionParams{Executor: exec})
	err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if h.Status() != StatusDone {
		t.Errorf("expected StatusDone, got %s", h.Status())
	}
	if calls := int(exec.calls.Load()); calls != 4 {
		t.Errorf("expected 4 calls (3 failures + 1 success), got %d", calls)
	}
}

func TestExecute_Cancel(t *testing.T) {
	svc := newTestService()
	defer svc.Shutdown()

	h := svc.Execute(ActionParams{Executor: &slowExecutor{duration: 10 * time.Second}})

	// Give it a moment to start
	time.Sleep(20 * time.Millisecond)

	h.Cancel()
	_ = h.Wait(context.Background()) // Canceled actions may or may not set an error
	status := h.Status()
	if status != StatusCanceled && status != StatusStalled {
		t.Errorf("expected StatusCanceled, got %s", status)
	}
}

func TestExecute_StalledDetection(t *testing.T) {
	svc := newTestService(func(cfg *ServiceConfig) {
		cfg.StalledTimeout = 100 * time.Millisecond
		cfg.WatchInterval = 30 * time.Millisecond
	})
	defer svc.Shutdown()

	h := svc.Execute(ActionParams{Executor: &slowExecutor{duration: 5 * time.Second}})

	// Wait for stalled detection (100ms timeout + 30ms check interval)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = h.Wait(ctx)

	status := h.Status()
	if status != StatusStalled && status != StatusCanceled {
		t.Errorf("expected StatusStalled or StatusCanceled, got %s", status)
	}
}

func TestExecute_ConcurrentActions(t *testing.T) {
	svc := newTestService(func(cfg *ServiceConfig) {
		cfg.WorkerCount = 8
	})
	defer svc.Shutdown()

	const n = 50
	exec := &counterExecutor{}
	handles := make([]*ActionHandle, n)

	for i := range n {
		handles[i] = svc.Execute(ActionParams{Executor: exec})
	}

	// Wait for all
	for i, h := range handles {
		if err := h.Wait(context.Background()); err != nil {
			t.Errorf("action %d failed: %v", i, err)
		}
	}

	if count := int(exec.count.Load()); count != n {
		t.Errorf("expected %d executions, got %d", n, count)
	}
}

func TestExecute_DataPassthrough(t *testing.T) {
	svc := newTestService()
	defer svc.Shutdown()

	type myData struct{ Value string }

	var received any
	exec := &dataCapture{captured: &received}
	h := svc.Execute(ActionParams{
		Executor: exec,
		Data:     &myData{Value: "hello"},
	})
	_ = h.Wait(context.Background())

	if received == nil {
		t.Fatal("data was not captured")
	}
	d, ok := received.(*myData)
	if !ok {
		t.Fatalf("expected *myData, got %T", received)
	}
	if d.Value != "hello" {
		t.Errorf("expected 'hello', got %q", d.Value)
	}
}

type dataCapture struct {
	captured *any
}

func (e *dataCapture) Execute(_ context.Context, actx *ActionContext) error {
	*e.captured = actx.Data
	return nil
}
func (e *dataCapture) Name(_ *ActionContext) string              { return "capture" }
func (e *dataCapture) RetryDelay(_ *ActionContext) time.Duration { return -1 }

func TestActiveCount(t *testing.T) {
	svc := newTestService(func(cfg *ServiceConfig) {
		cfg.WorkerCount = 2
	})
	defer svc.Shutdown()

	// Submit slow actions
	h1 := svc.Execute(ActionParams{Executor: &slowExecutor{duration: 200 * time.Millisecond}})
	h2 := svc.Execute(ActionParams{Executor: &slowExecutor{duration: 200 * time.Millisecond}})

	time.Sleep(20 * time.Millisecond)
	count := svc.ActiveCount()
	if count < 1 {
		t.Errorf("expected at least 1 active action, got %d", count)
	}

	_ = h1.Wait(context.Background())
	_ = h2.Wait(context.Background())

	// After completion, active count should be 0
	time.Sleep(20 * time.Millisecond)
	count = svc.ActiveCount()
	if count != 0 {
		t.Errorf("expected 0 active actions after completion, got %d", count)
	}
}

func TestShutdown_CancelsWorkers(t *testing.T) {
	svc := newTestService()

	h := svc.Execute(ActionParams{Executor: &slowExecutor{duration: 10 * time.Second}})

	time.Sleep(20 * time.Millisecond)
	svc.Shutdown()

	// After shutdown, the action should have completed or been canceled
	status := h.Status()
	if status == StatusPending || status == StatusRunning {
		t.Errorf("expected action to be resolved after shutdown, got %s", status)
	}
}

func TestActionStatus_String(t *testing.T) {
	tests := []struct {
		status ActionStatus
		want   string
	}{
		{StatusPending, "PENDING"},
		{StatusRunning, "RUNNING"},
		{StatusDone, "DONE"},
		{StatusFailed, "FAILED"},
		{StatusCanceled, "CANCELED"},
		{StatusStalled, "STALLED"},
		{ActionStatus(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("ActionStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
