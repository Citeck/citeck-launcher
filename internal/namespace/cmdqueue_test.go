package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainCollect runs Drain on q starting with first and returns the surviving
// commands in FIFO order.
func drainCollect(q *CmdQueue, first runtimeCmd) []runtimeCmd {
	var got []runtimeCmd
	q.Drain(first, func(c runtimeCmd) { got = append(got, c) })
	return got
}

func TestCmdQueueCoalescesStartStart(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStart{}))
	got := drainCollect(q, cmdStart{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdStart)
	assert.True(t, ok)
}

func TestCmdQueueCoalescesStopStop(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStop{}))
	got := drainCollect(q, cmdStop{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdStop)
	assert.True(t, ok)
}

func TestCmdQueueCoalescesStartStop(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStop{}))
	got := drainCollect(q, cmdStart{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdStop)
	assert.True(t, ok, "Start+Stop must collapse to Stop")
}

func TestCmdQueueCoalescesRegenerateRegenerate(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdRegenerate{}))
	got := drainCollect(q, cmdRegenerate{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdRegenerate)
	assert.True(t, ok)
}

func TestCmdQueueStartAbsorbsRegenerate(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdRegenerate{}))
	got := drainCollect(q, cmdStart{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdStart)
	assert.True(t, ok, "Start + Regenerate must keep Start (Start already covers regen)")
}

func TestCmdQueueRegenerateReplacedByStart(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStart{}))
	got := drainCollect(q, cmdRegenerate{})
	assert.Len(t, got, 1)
	_, ok := got[0].(cmdStart)
	assert.True(t, ok)
}

func TestCmdQueueCoalescesStopAppSameName(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStopApp{name: "alfresco"}))
	got := drainCollect(q, cmdStopApp{name: "alfresco"})
	assert.Len(t, got, 1)
	v, ok := got[0].(cmdStopApp)
	require.True(t, ok)
	assert.Equal(t, "alfresco", v.name)
}

func TestCmdQueuePreservesDifferentApps(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdStopApp{name: "alfresco"}))
	got := drainCollect(q, cmdStopApp{name: "share"})
	assert.Len(t, got, 2, "stopApp on different apps must NOT coalesce")
	assert.Equal(t, "share", got[0].(cmdStopApp).name)
	assert.Equal(t, "alfresco", got[1].(cmdStopApp).name)
}

func TestCmdQueueCoalescesRetryPullFailed(t *testing.T) {
	q := NewCmdQueue()
	require.NoError(t, q.Enqueue(cmdRetryPullFailed{}))
	require.NoError(t, q.Enqueue(cmdRetryPullFailed{}))
	got := drainCollect(q, cmdRetryPullFailed{})
	assert.Len(t, got, 1)
}

func TestCmdQueueFIFOForNonCoalescing(t *testing.T) {
	q := NewCmdQueue()
	// Mix of commands that don't coalesce as adjacent pairs.
	require.NoError(t, q.Enqueue(cmdStopApp{name: "b"}))
	require.NoError(t, q.Enqueue(cmdRetryPullFailed{}))
	got := drainCollect(q, cmdStopApp{name: "a"})
	require.Len(t, got, 3)
	assert.Equal(t, "a", got[0].(cmdStopApp).name)
	assert.Equal(t, "b", got[1].(cmdStopApp).name)
	_, ok := got[2].(cmdRetryPullFailed)
	assert.True(t, ok)
}

func TestCmdQueueBackpressureReturnsErr(t *testing.T) {
	q := NewCmdQueue()
	// Fill the buffer (256 capacity).
	for range cmdQueueCapacity {
		require.NoError(t, q.Enqueue(cmdRetryPullFailed{}))
	}
	// One more enqueue must time out and return ErrCmdQueueFull.
	err := q.Enqueue(cmdRetryPullFailed{})
	assert.ErrorIs(t, err, ErrCmdQueueFull)
}

func TestCmdQueueDrainProcessesAllSurvivors(t *testing.T) {
	q := NewCmdQueue()
	// Three coalescible Starts + a StopApp(x) + a StopApp(y) (don't coalesce
	// with each other) + a Regenerate which gets absorbed into the trailing
	// Start? No — Start absorbs trailing Regen; we want to check ordering.
	require.NoError(t, q.Enqueue(cmdStart{})) // collapses with next Start
	require.NoError(t, q.Enqueue(cmdStart{}))
	require.NoError(t, q.Enqueue(cmdStopApp{name: "x"}))
	require.NoError(t, q.Enqueue(cmdStopApp{name: "y"}))
	got := drainCollect(q, cmdStart{})
	// Expected after coalescing adjacent pairs: Start, StopApp(x), StopApp(y).
	require.Len(t, got, 3)
	_, ok := got[0].(cmdStart)
	assert.True(t, ok)
	assert.Equal(t, "x", got[1].(cmdStopApp).name)
	assert.Equal(t, "y", got[2].(cmdStopApp).name)
}
