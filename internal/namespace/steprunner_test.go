package namespace

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepOncePanicsInProductionMode(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := NewRuntime(cfg, newMockDocker(), t.TempDir())
	defer close(r.eventCh)
	assert.False(t, r.testMode)
	assert.Panics(t, func() { r.StepOnce() })
}

func TestRunUntilQuiescentReturnsErrorOnMaxSteps(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := newRuntimeForTest(cfg, newMockDocker(), t.TempDir())
	defer close(r.eventCh)
	// maxSteps=0 cannot settle by definition: the loop body never runs and
	// the loop falls through to the error return. Asserts the contract that
	// RunUntilQuiescent returns ErrMaxStepsExceeded (NOT panic / NOT t.Fatal)
	// when budget is exhausted.
	results, err := r.RunUntilQuiescent(0)
	require.ErrorIs(t, err, ErrMaxStepsExceeded)
	assert.Empty(t, results)

	// Now show the same with a non-trivial budget against a runtime that
	// stays busy: prepend a command before each tick so StepOnce always
	// reports Quiescent=false.
	r2 := newRuntimeForTest(cfg, newMockDocker(), t.TempDir())
	defer close(r2.eventCh)
	steps := 0
	const maxSteps = 5
	var lastErr error
	for range maxSteps {
		r2.InjectCmd(cmdStopApp{name: "x"})
		res := r2.StepOnce()
		steps++
		if res.Quiescent {
			lastErr = errors.New("did not expect quiescent")
			break
		}
	}
	require.NoError(t, lastErr)
	assert.Equal(t, maxSteps, steps)
}

func TestInjectCmdRoutesThroughQueue(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := newRuntimeForTest(cfg, newMockDocker(), t.TempDir())
	defer close(r.eventCh)
	r.InjectCmd(cmdStart{})
	res := r.StepOnce()
	assert.False(t, res.Quiescent)
	require.Len(t, res.EventsEmitted, 1)
	assert.Equal(t, "cmd_observed", res.EventsEmitted[0].Type)
	assert.Equal(t, "start", res.EventsEmitted[0].After)
}

func TestAdvanceClockMutatesFakeClock(t *testing.T) {
	start := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(start)
	cfg := &Config{ID: "ns1"}
	r := newRuntimeForTest(cfg, newMockDocker(), t.TempDir(), WithTestClock(fc))
	defer close(r.eventCh)
	assert.Equal(t, start, r.nowFunc())
	r.AdvanceClock(2 * time.Second)
	assert.Equal(t, start.Add(2*time.Second), r.nowFunc())
}

func TestAdvanceClockPanicsWithoutFakeClock(t *testing.T) {
	cfg := &Config{ID: "ns1"}
	r := newRuntimeForTest(cfg, newMockDocker(), t.TempDir())
	defer close(r.eventCh)
	assert.Panics(t, func() { r.AdvanceClock(time.Second) })
}
