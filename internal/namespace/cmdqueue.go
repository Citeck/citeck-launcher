// Package namespace — typed command queue with coalescing and back-pressure.
//
// CmdQueue is the single producer→single consumer FIFO that delivers external
// intent (Start, Stop, Restart, …) to runtimeLoop. Buffer 256 + 500ms
// back-pressure on Enqueue gives callers a typed error instead of a silent drop
// when the loop is wedged. Drain applies coalescing to adjacent pairs.
package namespace

import (
	"errors"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// runtimeCmd is the marker interface implemented by every command type pushed
// through CmdQueue. cmdTag returns a short discriminator for logging /
// coalesce-table dispatch.
type runtimeCmd interface {
	cmdTag() string
}

// cmdStart starts the namespace with the given desired app set.
type cmdStart struct {
	apps []appdef.ApplicationDef
}

func (cmdStart) cmdTag() string { return "start" }

// cmdStop initiates a full graceful shutdown.
type cmdStop struct{}

func (cmdStop) cmdTag() string { return "stop" }

// cmdStopApp marks a single app as detached + STOPPED.
type cmdStopApp struct {
	name string
}

func (cmdStopApp) cmdTag() string { return "stopApp" }

// cmdStartApp re-attaches a previously detached app.
type cmdStartApp struct {
	name string
}

func (cmdStartApp) cmdTag() string { return "startApp" }

// cmdRestartApp restarts a single app (delegates to cmdRegenerate when the app
// depends on a detached app).
type cmdRestartApp struct {
	name string
}

func (cmdRestartApp) cmdTag() string { return "restartApp" }

// cmdRegenerate rebuilds the desired-set / configs and reconciles existing
// containers (changed-hash → recreate, new → start, removed → STOPPED + GC).
type cmdRegenerate struct {
	apps      []appdef.ApplicationDef
	cfg       *Config
	bundleDef *bundle.Def
}

func (cmdRegenerate) cmdTag() string { return "regenerate" }

// cmdRetryPullFailed wakes all PULL_FAILED apps to attempt a fresh pull now.
type cmdRetryPullFailed struct{}

func (cmdRetryPullFailed) cmdTag() string { return "retryPullFailed" }

// cmdDetach signals detach (binary upgrade): cancel all workers, exit loop,
// keep containers alive.
type cmdDetach struct{}

func (cmdDetach) cmdTag() string { return "detach" }

// cmdStopNextGroup is internal-only: produced by the shutdown-continuation
// chain. It is never enqueued externally — evaluateContinuations applies it
// inline via applyCommand.
type cmdStopNextGroup struct {
	idx    int
	groups [][]*AppRuntime
}

func (cmdStopNextGroup) cmdTag() string { return "stopNextGroup" }

// CmdQueue is a buffered, typed FIFO for runtime commands.
type CmdQueue struct {
	ch chan runtimeCmd
}

// cmdQueueCapacity bounds the in-flight queue depth before back-pressure kicks in.
const cmdQueueCapacity = 256

// cmdQueueEnqueueTimeout is the maximum time Enqueue waits before returning
// ErrCmdQueueFull. Producers can use this to surface HTTP 503 to the user.
const cmdQueueEnqueueTimeout = 500 * time.Millisecond

// ErrCmdQueueFull is returned by Enqueue if the queue stays full for longer
// than cmdQueueEnqueueTimeout.
var ErrCmdQueueFull = errors.New("cmdQueue full")

// NewCmdQueue constructs a CmdQueue.
func NewCmdQueue() *CmdQueue {
	return &CmdQueue{ch: make(chan runtimeCmd, cmdQueueCapacity)}
}

// Enqueue pushes a command, blocking up to cmdQueueEnqueueTimeout. Returns
// ErrCmdQueueFull on timeout.
func (q *CmdQueue) Enqueue(cmd runtimeCmd) error {
	select {
	case q.ch <- cmd:
		return nil
	case <-time.After(cmdQueueEnqueueTimeout):
		return ErrCmdQueueFull
	}
}

// Chan returns the receive end for use in select.
func (q *CmdQueue) Chan() <-chan runtimeCmd {
	return q.ch
}

// Drain processes `first` plus any commands currently buffered (non-blocking),
// applying collapseCommandsIfPossible to adjacent pairs and then calling apply
// on each surviving command in FIFO order.
func (q *CmdQueue) Drain(first runtimeCmd, apply func(runtimeCmd)) {
	collected := []runtimeCmd{first}
drain:
	for {
		select {
		case c := <-q.ch:
			collected = append(collected, c)
		default:
			break drain
		}
	}
	survivors := collected[:0]
	for _, c := range collected {
		if len(survivors) == 0 {
			survivors = append(survivors, c)
			continue
		}
		prev := survivors[len(survivors)-1]
		merged, ok := collapseCommandsIfPossible(prev, c)
		if ok {
			survivors[len(survivors)-1] = merged
		} else {
			survivors = append(survivors, c)
		}
	}
	for _, c := range survivors {
		apply(c)
	}
}

// collapseCommandsIfPossible coalesces adjacent command pairs. Returns the
// merged command + true if the pair can be coalesced, else (a, false).
func collapseCommandsIfPossible(a, b runtimeCmd) (runtimeCmd, bool) {
	switch av := a.(type) {
	case cmdStart:
		_ = av
		switch b.(type) {
		case cmdStart, cmdStop:
			return b, true
		case cmdRegenerate:
			// Start already covers regen; keep the earlier Start.
			return a, true
		}
	case cmdStop:
		_ = av
		switch b.(type) {
		case cmdStart, cmdStop:
			return b, true
		}
	case cmdRegenerate:
		_ = av
		switch b.(type) {
		case cmdStart, cmdRegenerate:
			return b, true
		}
	case cmdStopApp:
		switch bv := b.(type) {
		case cmdStopApp:
			if bv.name == av.name {
				return b, true
			}
		case cmdStartApp:
			if bv.name == av.name {
				return b, true
			}
		}
	case cmdStartApp:
		if bv, ok := b.(cmdStopApp); ok && bv.name == av.name {
			return b, true
		}
	case cmdRestartApp:
		if bv, ok := b.(cmdRestartApp); ok && bv.name == av.name {
			return b, true
		}
	case cmdRetryPullFailed:
		_ = av
		if _, ok := b.(cmdRetryPullFailed); ok {
			return b, true
		}
	}
	return a, false
}
