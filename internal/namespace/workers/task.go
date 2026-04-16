// Package workers defines worker task primitives used by the namespace runtime
// dispatcher. It is intentionally pure-Go with no dependency on the docker or
// namespace packages so that it can be imported from both without creating
// import cycles.
package workers

import "context"

// OpKind enumerates the operation kinds dispatched onto worker goroutines.
type OpKind int

// Worker operation kinds.
const (
	OpPull OpKind = iota
	OpStart
	OpStop
	OpInit
	OpProbe
	OpRemoveNetwork
	OpStats
	OpReconcileDiff
	OpLivenessProbe
	OpPostStartActions
)

// String returns a short discriminator suitable for log fields.
func (o OpKind) String() string {
	switch o {
	case OpPull:
		return "pull"
	case OpStart:
		return "start"
	case OpStop:
		return "stop"
	case OpInit:
		return "init"
	case OpProbe:
		return "probe"
	case OpRemoveNetwork:
		return "removeNetwork"
	case OpStats:
		return "stats"
	case OpReconcileDiff:
		return "reconcileDiff"
	case OpLivenessProbe:
		return "livenessProbe"
	case OpPostStartActions:
		return "postStartActions"
	default:
		return "unknown"
	}
}

// TaskID uniquely identifies a (per-app, per-op) task slot in the dispatcher.
// Two Dispatch calls with the same TaskID supersede each other.
type TaskID struct {
	App string
	Op  OpKind
}

// CancelReason explains why a task's context was canceled. Plumbed through the
// dispatcher so that applyWorkerResult can drop or process canceled Results
// based on the cancellation cause.
type CancelReason int

// Cancellation reasons.
const (
	CancelNone CancelReason = iota
	CancelSuperseded
	CancelDetach
	CancelStopApp
	CancelExternalStop
)

// Result is the value posted to the runtime's resultCh after a worker finishes.
// TaskID + AttemptID are stamped by the dispatcher (workers do not see them);
// Payload is op-specific.
type Result struct {
	TaskID    TaskID
	AttemptID int64
	Err       error
	Payload   any
}

// TaskFunc is the unit of work a Dispatcher invokes on a goroutine.
type TaskFunc func(ctx context.Context) Result

// PullPayload carries pull-side data (image digest, etc.).
type PullPayload struct {
	Digest string
}

// StartPayload carries the started container's id.
type StartPayload struct {
	ContainerID string
}

// StopPayload is empty today; reserved for future fields (graceful flag, etc.).
type StopPayload struct{}

// InitPayload describes the init-container that ran (its index in the chain).
type InitPayload struct {
	// Index is the 0-based position in InitContainers. handleInitResult uses
	// it to decide T11 (dispatch next) vs T12 (dispatch startContainer).
	Index int
}

// ProbePayload is empty today; reserved.
type ProbePayload struct{}

// RemoveNetworkPayload reserved for later phases.
type RemoveNetworkPayload struct{}

// StatsPayload carries CPU and memory usage strings (display-formatted) for
// the target container. CPU is e.g. "1.5%"; Memory is e.g. "120M / 512M".
type StatsPayload struct {
	CPU    string
	Memory string
}

// MissingApp describes a running app whose container has disappeared from
// Docker's running-set, as observed by a ReconcileDiffTask. OOMKilled is set
// when docker inspect reports the container's State.OOMKilled flag.
type MissingApp struct {
	Name        string
	ContainerID string
	OOMKilled   bool
}

// ReconcileDiffPayload carries the list of apps whose containers are missing
// from Docker's running-set. Consumed by handleReconcileDiffResult to apply
// T18 (RUNNING → READY_TO_PULL with restart_event).
type ReconcileDiffPayload struct {
	Missing []MissingApp
}

// LivenessProbePayload reports the outcome of one liveness probe invocation.
// Consumed by handleLivenessProbeResult to increment/reset the
// livenessFailures counter and fire T17a on threshold.
type LivenessProbePayload struct {
	Healthy bool
}

// PostStartActionsPayload is empty — post-start init actions are best-effort
// side effects. Errors and non-zero exits are logged by the worker but do not
// flow into the state machine.
type PostStartActionsPayload struct{}
