package migrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// Captured from real containers on 2026-09-23 and trimmed to the fields the
// wait reads: a v1.14.1 caught in the middle of an index build (yellow), and
// the SAME collection after its server was stopped mid-build and booted on
// v1.16.3 (grey — pending, paused, and staying that way: nothing resumes it
// without an update, measured on 1.14.1, 1.15.5 and 1.16.3 alike).
const (
	realQdrantYellowOut = `{"result":{"status":"yellow","optimizer_status":"ok","indexed_vectors_count":26000,` +
		`"points_count":103000,"segments_count":2},"status":"ok","time":5.0125e-05}`
	realQdrantGreyAfterRestartOut = `{"result":{"status":"grey","optimizer_status":"ok","indexed_vectors_count":26000,` +
		`"points_count":80000,"segments_count":3},"status":"ok","time":0.000245833}`
)

// qdrantCollectionOut is a GET /collections/{name} answer with the two fields
// the wait reads, for the states no real container could be made to show on
// demand. optimizer is raw JSON: the schema is `"ok"` or `{"error": "..."}`.
func qdrantCollectionOut(status, optimizer string) string {
	return fmt.Sprintf(`{"result":{"status":%q,"optimizer_status":%s,"points_count":3},"status":"ok"}`,
		status, optimizer)
}

// optimizerScript serves ONE collection, "docs", whose state is the next
// answer in the queue on every request; the last answer repeats forever.
type optimizerScript struct {
	mu      sync.Mutex
	answers []string
	asked   int
}

func (s *optimizerScript) exec(_, cmdline string) (stdout, stderr string, exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.HasSuffix(cmdline, " /collections"):
		return `{"result":{"collections":[{"name":"docs"}]},"status":"ok"}`, "", 0, nil
	case strings.HasSuffix(cmdline, " /collections/docs"):
		i := min(s.asked, len(s.answers)-1)
		s.asked++
		return s.answers[i], "", 0, nil
	}
	return "", "unexpected " + cmdline, 1, nil
}

func (s *optimizerScript) times() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

// fastOptimizerWait shrinks the wait's cadence and deadline for one test.
func fastOptimizerWait(t *testing.T, timeout time.Duration) {
	t.Helper()
	origTimeout, origPoll := qdrantOptimizerTimeout, qdrantOptimizerPoll
	qdrantOptimizerTimeout, qdrantOptimizerPoll = timeout, time.Millisecond
	t.Cleanup(func() { qdrantOptimizerTimeout, qdrantOptimizerPoll = origTimeout, origPoll })
}

// progressLog records what a step reported.
type progressLog struct {
	mu    sync.Mutex
	lines []msg.Message
}

func (l *progressLog) report(_ float64, m msg.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, m)
}

func (l *progressLog) all() []msg.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]msg.Message(nil), l.lines...)
}

func waitOptimizersIn(t *testing.T, answers []string, p StepProgress) (*optimizerScript, error) {
	t.Helper()
	s := &optimizerScript{answers: answers}
	env := qdrantInventoryEnv(t, &execScript{})
	env.ExecFn = s.exec
	return s, waitForQdrantOptimizers(context.Background(), env, SrcContainer, p)
}

// The ordinary case, and the one every real migration measured so far was: no
// collection optimizing, so the wait answers on its first look and says
// nothing. Grey counts as settled — a wait for green would hang every volume
// whose optimizations were pending when it stopped.
func TestQdrantOptimizerWaitPassesGreenAndGreyAtOnce(t *testing.T) {
	fastOptimizerWait(t, 50*time.Millisecond)
	for name, answer := range map[string]string{
		"green":               realQdrantDocsOut,
		"grey on the 1.15":    realQdrantDocsAfterOut,
		"grey after an abort": realQdrantGreyAfterRestartOut,
	} {
		t.Run(name, func(t *testing.T) {
			var progress progressLog
			s, err := waitOptimizersIn(t, []string{answer}, progress.report)
			require.NoError(t, err)
			assert.Equal(t, 1, s.times(), "one look is enough when nothing is running")
			assert.Empty(t, progress.all(), "a wait that did not wait reports nothing")
		})
	}
}

// A running optimization is waited out, and the operator is told which
// collection the migration is waiting on.
func TestQdrantOptimizerWaitOutlastsARunningOptimization(t *testing.T) {
	fastOptimizerWait(t, time.Minute)
	var progress progressLog
	s, err := waitOptimizersIn(t,
		[]string{realQdrantYellowOut, realQdrantYellowOut, realQdrantGreyAfterRestartOut}, progress.report)
	require.NoError(t, err)
	assert.Equal(t, 3, s.times())
	lines := progress.all()
	require.NotEmpty(t, lines)
	assert.Equal(t, "waiting for Qdrant in "+SrcContainer+" to finish optimizing: docs", oneEN(lines[0]))
}

// An optimization that never ends fails the step at the deadline, naming what
// was still running — the migration then rolls back to an untouched original.
func TestQdrantOptimizerWaitGivesUpAtItsDeadline(t *testing.T) {
	fastOptimizerWait(t, 20*time.Millisecond)
	_, err := waitOptimizersIn(t, []string{realQdrantYellowOut}, func(float64, msg.Message) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still optimizing docs")
	assert.Contains(t, err.Error(), SrcContainer)
}

// What will not settle by being asked again fails at ONCE rather than after
// half an hour: an optimizer error, a red collection, and anything this
// launcher cannot read — an unknown status is not evidence that the data is
// safe to hand to the next minor.
func TestQdrantOptimizerWaitRefusesWhatWillNotSettle(t *testing.T) {
	// Short, so a wait that retried a verdict instead of failing on it fails
	// this test in milliseconds rather than blocking the suite.
	fastOptimizerWait(t, 50*time.Millisecond)
	for name, tc := range map[string]struct{ answer, want string }{
		"optimizer error": {qdrantCollectionOut("yellow", `{"error":"No space left on device"}`),
			"the optimizer reports an error: No space left on device"},
		"red":                      {qdrantCollectionOut("red", `"ok"`), `status "red"`},
		"unknown status":           {qdrantCollectionOut("purple", `"ok"`), `status "purple"`},
		"unknown optimizer string": {qdrantCollectionOut("green", `"paused"`), `optimizer status "paused"`},
		"unreadable optimizer":     {qdrantCollectionOut("green", `5`), "unreadable optimizer status 5"},
		"not JSON at all":          {"<html>502 Bad Gateway</html>", "GET /collections/docs"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := waitOptimizersIn(t, []string{tc.answer}, func(float64, msg.Message) {})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, 1, s.times(), "a verdict that cannot change is not asked for twice")
		})
	}
}

// The hook is wired where it has to be: a node still optimizing is never
// replaced. The first rung is not even started, and the rollback deletes the
// copy — the namespace's own volume is untouched (guardEnv).
func TestQdrantDoesNotClimbPastANodeThatIsStillOptimizing(t *testing.T) {
	fastOptimizerWait(t, 20*time.Millisecond)
	s := realQdrantScript()
	s.out[SrcContainer+"|/collections/docs"] = realQdrantYellowOut
	env := qdrantEnv(t, s)

	err := runQdrantPlan(t, env, qdrantFrom, qdrantTo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still optimizing docs")
	for _, line := range env.Log() {
		assert.NotContains(t, line, "run:"+DstContainer, "the next rung must not be started")
	}
	d := qdrantDescriptorOf(t)
	assert.NotContains(t, env.Volumes, deps.VolumeName(d, 2), "the rollback removes the copy")
	assert.Contains(t, env.Volumes, deps.VolumeName(d, 1), "the source volume is only ever read")
}

// On a ladder the wait runs on every node that is about to be replaced, not
// only the bottom one: an intermediate node whose optimizer failed stops the
// climb before the rung above it is started.
func TestQdrantStopsTheLadderAtAnIntermediateNodeThatWillNotSettle(t *testing.T) {
	const top = "qdrant/qdrant:v1.16.1"
	s := realQdrantScript()
	s.out[DstContainer+"|/collections/docs"] = qdrantCollectionOut("green", `{"error":"segment is corrupted"}`)
	env := qdrantEnv(t, s)

	plan, j, err := (QdrantMigrator{ID: deps.Qdrant}).Plan(context.Background(), env,
		Path{qdrantFrom, qdrantTo, top}, PlanOptions{})
	require.NoError(t, err)
	err = Run(context.Background(), j2store(), j, plan, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "segment is corrupted")
	log := strings.Join(env.Log(), "\n")
	assert.Contains(t, log, "run:"+DstContainer+":"+qdrantTo, "the middle rung was started")
	assert.NotContains(t, log, "run:"+DstContainer+":"+top,
		"the rung above a node that will not settle is never started (it was only pulled, like every rung)")
}

// The progress line is TRANSLATED, so the list in it carries no English: the
// "and N more" of namesPreview belongs in the errors we read ourselves.
func TestListPreviewCarriesNoEnglish(t *testing.T) {
	names := make([]string, maxNamedItems+5)
	for i := range names {
		names[i] = fmt.Sprintf("c%02d", i)
	}
	got := listPreview(names)
	assert.True(t, strings.HasSuffix(got, ", …"))
	assert.NotContains(t, got, "more")
	assert.NotContains(t, got, names[maxNamedItems])
	assert.Equal(t, "a, b", listPreview([]string{"a", "b"}))
}
