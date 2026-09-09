package namespace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// startedLoopRuntime brings a real runtimeLoop up on one app and waits until it
// is idle, so a write armed after it returns is the only thing the loop still
// owes. Everything about the persist-debt contract is a property of the LOOP —
// a unit test of the helper cannot tell "retried once" from "retried until it
// lands" — so these tests drive the real thing.
func startedLoopRuntime(t *testing.T, p NsStatePersister) *Runtime {
	t.Helper()
	r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
	r.tickerPeriod = 20 * time.Millisecond
	// The retry cadence is not what these tests are about; keep it out of
	// their wall clock (TestTheTailRetryBacksOffWhileTheStoreIsBroken owns it).
	r.persistRetryBase = 5 * time.Millisecond
	r.SetStatePersister(p)
	t.Cleanup(r.Shutdown)

	r.Start([]appdef.ApplicationDef{simpleApp("gateway", "gw:1")}, false)
	require.True(t, waitForAppStatus(r, "gateway", AppStatusRunning, 10*time.Second),
		"gateway did not reach RUNNING")
	require.True(t, waitUntil(5*time.Second, func() bool { return !r.dirty.Load() }),
		"the loop never went idle")
	return r
}

// The debt survives more than one failed write. Clearing r.dirty after the
// tail's OWN failed persist caps the retry at "once per marking": the first
// retry fails, the debt is dropped with it, and the state file keeps the
// pre-mutation content for good — a store that is unavailable for two seconds
// loses the record exactly as thoroughly as one that is unavailable forever.
func TestTheLoopTailRetriesUntilTheWriteLands(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	// The inline write plus the next two tail retries.
	p.arm(3)
	r.SetDependencyPin(deps.Postgres, "postgres:17.5")

	require.True(t, waitUntil(10*time.Second, func() bool {
		var st NsPersistedState
		return json.Unmarshal([]byte(p.lastJSON()), &st) == nil &&
			st.Dependencies[deps.Postgres].Image == "postgres:17.5"
	}), "the loop tail gave up on the write before the store came back")

	_, armed, failed := p.stats()
	require.Equal(t, 0, armed, "every armed failure must have been spent on a real attempt")
	require.Equal(t, 3, failed)
}

// syncBuffer is a log sink safe to read while the runtimeLoop goroutine is
// still writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log line: %w", err)
	}
	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// countLines counts full log lines carrying both substrings.
func countLines(buf *syncBuffer, a, b string) int {
	n := 0
	for ln := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(ln, a) && strings.Contains(ln, b) {
			n++
		}
	}
	return n
}

// captureLogs redirects slog to a buffer for the duration of the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// Keeping the debt marked means the loop tail retries on EVERY iteration —
// ticker, command, worker result, signal — so a store that is permanently
// broken (a full disk) would otherwise put a WARN in the daemon log several
// times a second, for as long as the daemon runs, burying whatever the
// operator is actually looking for. One line per failure STREAK says the same
// thing; the recovery line is what tells the operator the streak ended, and it
// carries how many writes were refused, which a per-attempt log would only
// have stated by arithmetic.
func TestAFailingStoreIsLoggedOncePerStreakNotOncePerAttempt(t *testing.T) {
	buf := captureLogs(t)
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	p := &armedFailPersister{}
	r.SetStatePersister(p)

	persist := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.persistUnderLock("loop-tail")
	}

	p.arm(5)
	for i := range 5 {
		require.Error(t, persist(), "attempt %d", i+1)
	}
	require.Equal(t, 1, countLines(buf, "level=WARN", "Failed to persist namespace state"),
		"a failing store must be reported once per streak, not once per attempt")
	assert.Contains(t, buf.String(), "nsX", "the line must name the namespace that owes the write")

	// The store comes back: the operator is told, and how much was refused.
	require.NoError(t, persist())
	require.Equal(t, 1, countLines(buf, "level=INFO", "Namespace state write recovered"),
		"the end of the streak is the other half of the report")
	assert.Contains(t, buf.String(), "refusedWrites=5")

	// A NEW streak is a new event, not a continuation of the old one.
	p.arm(1)
	require.Error(t, persist())
	assert.Equal(t, 2, countLines(buf, "level=WARN", "Failed to persist namespace state"),
		"a store that breaks again after recovering must be reported again")
	require.NoError(t, persist())
	assert.Equal(t, 2, countLines(buf, "level=INFO", "Namespace state write recovered"))
}

// The streak counter is per Runtime, so one namespace's broken store cannot
// mute the report of another's — and a fresh process starts every namespace at
// zero, so the first failure after a restart is always reported.
func TestThePersistFailureStreakIsPerNamespace(t *testing.T) {
	buf := captureLogs(t)
	newFailing := func(id string) *Runtime {
		r := NewRuntime(&Config{ID: id}, nil, t.TempDir())
		r.SetStatePersister(failingPersister{})
		return r
	}
	a, b := newFailing("nsA"), newFailing("nsB")
	for _, r := range []*Runtime{a, b, a, b} {
		r.mu.Lock()
		require.Error(t, r.persistUnderLock("loop-tail"))
		r.mu.Unlock()
	}
	assert.Equal(t, 2, countLines(buf, "level=WARN", "Failed to persist namespace state"),
		"each namespace reports its own first failure")
	assert.Equal(t, 1, countLines(buf, "namespace=nsA", "Failed to persist namespace state"))
	assert.Equal(t, 1, countLines(buf, "namespace=nsB", "Failed to persist namespace state"))
}

// The retry is per iteration, so a permanently broken store would have the
// loop attempt a write every tick, every command and every worker result —
// each one holding r.mu for the store's full latency. Measured against the
// real loop, a store that takes 100 ms to fail cost 65% of the loop's wall
// clock and made a concurrent r.Status() wait the whole 100 ms; a hung network
// FS would stop the state machine outright. The backoff keeps the debt real
// and caps what a broken store costs the loop.
func TestTheTailRetryBacksOffWhileTheStoreIsBroken(t *testing.T) {
	fc := NewFakeClock(time.Unix(1_700_000_000, 0))
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.nowFunc = fc.Now
	r.persistRetryBase = time.Second
	r.SetStatePersister(failingPersister{})

	persist := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.persistUnderLock("loop-tail")
	}
	due := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.persistRetryDueUnderLock()
	}

	require.True(t, due(), "with no failure streak open the tail is never delayed")

	require.Error(t, persist())
	assert.False(t, due(), "the tail must not re-attempt in the same instant")
	fc.Advance(time.Second - time.Millisecond)
	assert.False(t, due(), "still inside the first delay")
	fc.Advance(time.Millisecond)
	assert.True(t, due(), "one base after the failure the retry is due")

	require.Error(t, persist())
	fc.Advance(2*time.Second - time.Millisecond)
	assert.False(t, due(), "the second failure doubles the delay")
	fc.Advance(time.Millisecond)
	assert.True(t, due())

	// The delay is capped: a store that has been broken for hours is still
	// retried, just not more often than persistRetryMax.
	for range 20 {
		require.Error(t, persist())
	}
	fc.Advance(persistRetryMax - time.Millisecond)
	assert.False(t, due())
	fc.Advance(time.Millisecond)
	assert.True(t, due(), "the delay must saturate, not grow without bound")

	// A write that lands ends the streak, and with it the gate.
	r.SetStatePersister(&fakePersister{})
	require.NoError(t, persist())
	assert.True(t, due(), "no streak, no delay: an ordinary tail persist is never gated")
}

// The call-site half: the loop tail must consult the gate. Without it the loop
// re-attempts the refused write on every iteration — with a 20 ms ticker, ~25
// attempts in the window below.
func TestTheLoopTailDoesNotSpinOnABrokenStore(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)
	r.mu.Lock()
	r.persistRetryBase = 10 * time.Second
	r.mu.Unlock()

	p.arm(1 << 30)
	before, _, _ := p.stats()
	r.SetDependencyPin(deps.Postgres, "postgres:17.5")
	time.Sleep(500 * time.Millisecond)
	after, _, _ := p.stats()

	assert.Equal(t, 1, after-before,
		"only the inline write may be attempted; the tail must wait out the backoff")
	assert.True(t, r.dirty.Load(), "and the write is still owed")
}

// StopApp was read as an exit path ("it stops the last app"), which it is not:
// nothing about detaching an app closes shutdownComplete, so the loop keeps
// iterating and the tail is there to retry. This is the call-site half of the
// mutator contract — the unit tests assert the flag, this asserts that
// something acts on it.
func TestTheLoopTailRetriesADetachThatDidNotReachDisk(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	p.arm(1)
	require.NoError(t, r.StopApp("gateway"))
	_, armed, _ := p.stats()
	require.Equal(t, 0, armed, "the inline detach write must have been attempted")

	require.True(t, waitUntil(10*time.Second, func() bool {
		var st NsPersistedState
		return json.Unmarshal([]byte(p.lastJSON()), &st) == nil &&
			slices.Contains(st.ManualStoppedApps, "gateway")
	}), "the detach intent never reached the store after the first write was refused")
}

// Detach is the one path where the debt is deliberately NOT marked: it is the
// last write this runtime makes, and exactly one loop tail runs after it
// (measured — the loop finishes the iteration and only then sees the closed
// shutdownComplete), so a flag set here would buy one retry and then be
// abandoned. What the operator needs instead is to be told, because the next
// daemon adopts the running containers using whatever the last successful
// write said.
func TestADetachWriteThatFailedIsReportedNotSilentlyOwed(t *testing.T) {
	buf := captureLogs(t)
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	p.arm(1 << 30)
	r.Detach()
	require.True(t, waitUntil(10*time.Second, func() bool {
		return countLines(buf, "level=ERROR", "Namespace state was NOT saved on detach") == 1
	}), "a detach whose state write failed must be reported at ERROR")
	assert.Contains(t, buf.String(), "namespace=test")
	assert.False(t, r.dirty.Load(),
		"and must not leave a debt marked: after detach there is no loop left to collect it")
}
