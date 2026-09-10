package migrate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"

	"github.com/citeck/citeck-launcher/internal/msg"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// The shared fake Env lives in migratetest so the plan tests, the daemon
// crash-recovery tests and the integration harness reuse one implementation;
// the engine only has to know it really is an Env.
var _ Env = (*migratetest.FakeEnv)(nil)

type fakeStore struct {
	mu       sync.Mutex
	journal  *deps.MigrationJournal
	journals []deps.MigrationJournal // every SetMigrationJournal payload
	commits  []deps.MigrationResult
	failures []deps.MigrationResult
	rbFails  []deps.MigrationResult
	pin      deps.DependencyState
	setErr   error // injected SetMigrationJournal failure
	setErrAt int   // fail the Nth (1-based) SetMigrationJournal call
	setCalls int
	// onSet observes every persisted journal as it happens. It is how a test
	// interleaves journal writes with what the Env was asked to do — the two
	// are otherwise recorded in two unrelated sequences, and "written ahead of"
	// is a claim about their ORDER. It runs under the store's lock, so it must
	// only touch things outside the store.
	onSet func(deps.MigrationJournal)
}

func (s *fakeStore) MigrationJournal() *deps.MigrationJournal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journal == nil {
		return nil
	}
	c := *s.journal
	return &c
}

func (s *fakeStore) SetMigrationJournal(j *deps.MigrationJournal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCalls++
	if s.setErr != nil && s.setCalls == s.setErrAt {
		return s.setErr
	}
	if j == nil {
		s.journal = nil
		return nil
	}
	c := *j
	s.journal = &c
	s.journals = append(s.journals, c)
	if s.onSet != nil {
		s.onSet(c)
	}
	return nil
}

func (s *fakeStore) CommitMigration(_ deps.ID, st deps.DependencyState, res deps.MigrationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pin = st
	s.journal = nil
	s.commits = append(s.commits, res)
	return nil
}

func (s *fakeStore) RecordMigrationFailure(res deps.MigrationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journal = nil
	s.failures = append(s.failures, res)
	return nil
}

// RecordRollbackFailure records the verdict WITHOUT clearing the journal —
// the store's half of the "a failed rollback keeps the record" contract.
func (s *fakeStore) RecordRollbackFailure(res deps.MigrationResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rbFails = append(s.rbFails, res)
	return nil
}

func (s *fakeStore) steps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.journals))
	for _, j := range s.journals {
		out = append(out, j.Step)
	}
	return out
}

func step(id string, fn func(j *deps.MigrationJournal) error) Step {
	return Step{ID: id, Run: func(_ context.Context, j *Journal, _ StepProgress) error { return fn(&j.MigrationJournal) }}
}

func baseJournal() deps.MigrationJournal {
	return deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", StartedAt: time.Now()}
}

func TestRunJournalsEveryStepThenCommitsOnce(t *testing.T) {
	st := &fakeStore{}
	var order []string
	plan := &Plan{
		Steps: []Step{
			step("a", func(*deps.MigrationJournal) error { order = append(order, "a"); return nil }),
			step("b", func(j *deps.MigrationJournal) error {
				j.CreatedVolume = "postgres3"
				order = append(order, "b")
				return nil
			}),
		},
		Rollback: func(context.Context, *deps.MigrationJournal) error { order = append(order, "rollback"); return nil },
		Result: func(j *deps.MigrationJournal) deps.MigrationResult {
			return deps.MigrationResult{ID: j.ID, OldVolume: "postgres2"}
		},
		Finalize: func(context.Context, *deps.MigrationJournal) error { order = append(order, "finalize"); return nil },
	}
	var progress []string
	err := Run(context.Background(), st, baseJournal(), plan, func(id string, _, _ int, _ float64, _ msg.Message) {
		progress = append(progress, id)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "finalize"}, order)
	assert.Equal(t, []string{"a", "b"}, progress)
	// write-ahead: "" before a, "a" after a, "b" after b
	require.Equal(t, []string{"", "a", "b"}, st.steps())
	// Fatal above, because the next line indexes what it just counted: a
	// non-fatal assert would report the real failure and then panic on it.
	assert.Equal(t, "postgres3", st.journals[2].CreatedVolume, "journal carries what steps record")
	require.Len(t, st.commits, 1)
	assert.Equal(t, "postgres2", st.commits[0].OldVolume)
	assert.False(t, st.commits[0].FinishedAt.IsZero(), "the engine stamps the result")
	assert.Equal(t, deps.DependencyState{Image: "postgres:18"}, st.pin)
	assert.Nil(t, st.journal)
	assert.Empty(t, st.failures)
}

// The commit is ONE write of the pair (image, generation), and both halves come
// from the JOURNAL — recorded when it was opened, before the migration started
// rewriting the world it would otherwise have to re-derive them from. A commit
// that moved the image but not the generation would leave the namespace
// mounting the OLD volume with the NEW image, which for PostgreSQL is 18 over a
// 17 data directory: an empty cluster's layout emitted onto real data.
func TestCommitTakesTheImageAndTheGenerationFromTheJournal(t *testing.T) {
	st := &fakeStore{}
	j := baseJournal()
	j.ToVolumeGen = 3
	j.SourceVolume = "postgres3"
	plan := &Plan{
		Steps:    []Step{step("a", func(*deps.MigrationJournal) error { return nil })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
		Result: func(j *deps.MigrationJournal) deps.MigrationResult {
			return deps.MigrationResult{OldVolume: j.SourceVolume}
		},
	}
	require.NoError(t, Run(context.Background(), st, j, plan, nil))
	assert.Equal(t, deps.DependencyState{Image: "postgres:18", VolumeGen: 3}, st.pin,
		"image and generation move together, from the journal")
	require.Len(t, st.commits, 1)
	assert.Equal(t, "postgres3", st.commits[0].OldVolume,
		"the journal names the volume the data came from, so the verdict can too")
}

// Progress is the engine's own bookkeeping: which step, 1-based, of how many,
// plus whatever sub-progress the step reports under that same identity. The
// web dialog renders "step 2 of 10" from it and the CLI draws its bar from the
// percent, so an off-by-one index, a total taken from anything but the plan,
// or a sub-report filed under the wrong step is a screen that lies about how
// far a migration has come — and none of that fails anything else.
func TestProgressNumbersEveryStepAndForwardsItsSubProgress(t *testing.T) {
	type report struct {
		id           string
		index, total int
		pct          float64
		msg          string
	}
	st := &fakeStore{}
	plan := &Plan{
		Steps: []Step{
			step("first", func(*deps.MigrationJournal) error { return nil }),
			{ID: "second", Run: func(_ context.Context, _ *Journal, p StepProgress) error {
				p(41.5, msg.New("deps.msg.progress.dumped", "size", "512.0 MiB"))
				p(99, msg.New("deps.msg.progress.dumped", "size", "1.2 GiB"))
				return nil
			}},
			step("third", func(*deps.MigrationJournal) error { return nil }),
		},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
		Result:   func(*deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{} },
	}
	var got []report
	require.NoError(t, Run(context.Background(), st, baseJournal(), plan,
		func(id string, index, total int, pct float64, m msg.Message) {
			// Rendered here, exactly as the daemon renders it for one SSE
			// subscriber: what the engine hands on is the MESSAGE, and the
			// sentence is what the operator ends up reading.
			got = append(got, report{id, index, total, pct, i18n.NewTranslator("en").Render(m)})
		}))
	assert.Equal(t, []report{
		{"first", 1, 3, 0, ""},
		{"second", 2, 3, 0, ""},
		{"second", 2, 3, 41.5, "dumped 512.0 MiB"},
		{"second", 2, 3, 99, "dumped 1.2 GiB"},
		{"third", 3, 3, 0, ""},
	}, got)
}

// A step reports its sub-progress unconditionally, so a caller that wants no
// progress at all must not make that a crash: the engine substitutes a no-op.
func TestAStepMayReportProgressWithoutAProgressCallback(t *testing.T) {
	st := &fakeStore{}
	plan := &Plan{
		Steps: []Step{{ID: "dump", Run: func(_ context.Context, _ *Journal, p StepProgress) error {
			p(50, msg.New("deps.msg.progress.analyzing"))
			return nil
		}}},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
		Result:   func(*deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{} },
	}
	require.NoError(t, Run(context.Background(), st, baseJournal(), plan, nil))
	require.Len(t, st.commits, 1)
}

// Ruling 5: the pin, the journal id and the result's identity are one source
// of truth — the journal — whatever the plan's Result left blank.
func TestCommitCarriesTheJournalsIdentity(t *testing.T) {
	st := &fakeStore{}
	plan := &Plan{
		Steps:    []Step{step("a", func(*deps.MigrationJournal) error { return nil })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
		Result:   func(*deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{OldVolume: "postgres2"} },
	}
	require.NoError(t, Run(context.Background(), st, baseJournal(), plan, nil))
	require.Len(t, st.commits, 1)
	assert.Equal(t, deps.Postgres, st.commits[0].ID)
	assert.Equal(t, "postgres:17.5", st.commits[0].From)
	assert.Equal(t, "postgres:18", st.commits[0].To)
	assert.Equal(t, deps.DependencyState{Image: "postgres:18"}, st.pin)
}

// A step that creates something records it and asks for a persist BEFORE
// creating it; the engine must write that journal there and then, not only
// once the step returns.
func TestAStepCanPersistTheJournalBeforeItActs(t *testing.T) {
	st := &fakeStore{}
	plan := &Plan{
		Steps: []Step{{ID: "create", Run: func(_ context.Context, j *Journal, _ StepProgress) error {
			j.CreatedVolume = "postgres3"
			if err := j.Persist(); err != nil {
				return err
			}
			// The write-ahead entry is visible to a crash-recovery reader now.
			cur := st.MigrationJournal()
			require.NotNil(t, cur)
			assert.Equal(t, "postgres3", cur.CreatedVolume)
			assert.Empty(t, cur.Step, "the step has not completed yet")
			return nil
		}}},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	require.NoError(t, Run(context.Background(), st, baseJournal(), plan, nil))
	assert.Equal(t, []string{"", "", "create"}, st.steps())
}

func TestStepFailureRollsBackWithTheJournalAsItWas(t *testing.T) {
	st := &fakeStore{}
	var seen *deps.MigrationJournal
	plan := &Plan{
		Steps: []Step{
			step("a", func(j *deps.MigrationJournal) error { j.CreatedVolume = "postgres3"; return nil }),
			step("b", func(*deps.MigrationJournal) error { return errors.New("dump failed") }),
			step("c", func(*deps.MigrationJournal) error { t.Error("must not run"); return nil }),
		},
		Rollback: func(_ context.Context, j *deps.MigrationJournal) error { c := *j; seen = &c; return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	err := Run(context.Background(), st, baseJournal(), plan, nil)
	require.ErrorContains(t, err, "dump failed")
	require.NotNil(t, seen)
	assert.Equal(t, "a", seen.Step)
	assert.Equal(t, "postgres3", seen.CreatedVolume)
	require.Len(t, st.failures, 1)
	assert.Contains(t, st.failures[0].Error, "dump failed")
	assert.Equal(t, deps.Postgres, st.failures[0].ID)
	assert.Equal(t, "postgres:17.5", st.failures[0].From)
	assert.Equal(t, "postgres:18", st.failures[0].To)
	assert.Empty(t, st.commits)
	assert.Empty(t, st.pin, "pin must not move on failure")
	assert.Empty(t, st.rbFails)
	assert.Nil(t, st.journal, "journal cleared after a clean rollback")
}

func TestRollbackFailureIsReportedAlongsideTheCause(t *testing.T) {
	st := &fakeStore{}
	plan := &Plan{
		Steps:    []Step{step("a", func(*deps.MigrationJournal) error { return errors.New("boom") })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { return errors.New("rm volume: busy") },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	err := Run(context.Background(), st, baseJournal(), plan, nil)
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "rm volume: busy")
	require.Len(t, st.rbFails, 1)
	assert.Contains(t, st.rbFails[0].Error, "boom")
	assert.Contains(t, st.rbFails[0].Error, "rollback failed")
	assert.Empty(t, st.failures, "a failed rollback does not close the migration")
	// The volume the rollback could not remove is still out there, so the
	// record of it must survive for the next start to retry.
	require.NotNil(t, st.journal, "a failed rollback keeps the journal")
	assert.Equal(t, deps.Postgres, st.journal.ID)
	assert.Empty(t, st.commits)
	assert.Empty(t, st.pin)
}

func TestCancelledContextRollsBackOnAnUncancelledOne(t *testing.T) {
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rollbackCtxErr error
	var hasDeadline bool
	plan := &Plan{
		Steps: []Step{
			step("a", func(*deps.MigrationJournal) error { cancel(); return nil }), // daemon shutdown mid-step
			step("b", func(*deps.MigrationJournal) error { t.Error("must not run after cancel"); return nil }),
		},
		Rollback: func(rctx context.Context, _ *deps.MigrationJournal) error {
			rollbackCtxErr = rctx.Err()
			_, hasDeadline = rctx.Deadline()
			return nil
		},
		Result: func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	err := Run(ctx, st, baseJournal(), plan, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, rollbackCtxErr, "rollback context must not inherit the cancellation")
	assert.True(t, hasDeadline, "rollback runs under its own bounded timeout")
	require.Len(t, st.failures, 1)
	assert.Empty(t, st.commits)
}

// The commit is the point of no return, so a cancellation that lands after the
// LAST step still rolls back rather than moving the pin.
func TestCancellationAfterTheLastStepDoesNotCommit(t *testing.T) {
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rolledBack := false
	plan := &Plan{
		Steps:    []Step{step("only", func(*deps.MigrationJournal) error { cancel(); return nil })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { rolledBack = true; return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
		Finalize: func(context.Context, *deps.MigrationJournal) error {
			t.Error("no finalize without a commit")
			return nil
		},
	}
	err := Run(ctx, st, baseJournal(), plan, nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, rolledBack)
	assert.Empty(t, st.commits)
	assert.Empty(t, st.pin)
}

func TestJournalPersistFailureBeforeTheFirstStepRunsNothing(t *testing.T) {
	st := &fakeStore{setErr: errors.New("disk full"), setErrAt: 1}
	plan := &Plan{
		Steps:    []Step{step("a", func(*deps.MigrationJournal) error { t.Error("must not run"); return nil })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { t.Error("nothing to roll back"); return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	err := Run(context.Background(), st, baseJournal(), plan, nil)
	require.ErrorContains(t, err, "disk full")
	assert.Empty(t, st.commits)
	assert.Empty(t, st.failures)
}

// A journal that cannot be written after a step is as dangerous as a failed
// step: the record of what to undo is already stale, so the engine rolls back.
func TestJournalPersistFailureAfterAStepRollsBack(t *testing.T) {
	st := &fakeStore{setErr: errors.New("disk full"), setErrAt: 2}
	rolledBack := false
	plan := &Plan{
		Steps: []Step{
			step("a", func(*deps.MigrationJournal) error { return nil }),
			step("b", func(*deps.MigrationJournal) error { t.Error("must not run"); return nil }),
		},
		Rollback: func(context.Context, *deps.MigrationJournal) error { rolledBack = true; return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
	}
	err := Run(context.Background(), st, baseJournal(), plan, nil)
	require.ErrorContains(t, err, "disk full")
	assert.True(t, rolledBack)
	assert.Empty(t, st.commits)
}

func TestFinalizeFailureIsAFinalizeErrorAfterCommit(t *testing.T) {
	st := &fakeStore{}
	plan := &Plan{
		Steps:    []Step{step("a", func(*deps.MigrationJournal) error { return nil })},
		Rollback: func(context.Context, *deps.MigrationJournal) error { t.Error("no rollback after commit"); return nil },
		Result:   func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} },
		Finalize: func(context.Context, *deps.MigrationJournal) error { return errors.New("start failed") },
	}
	err := Run(context.Background(), st, baseJournal(), plan, nil)
	var fe *FinalizeError
	require.ErrorAs(t, err, &fe)
	require.ErrorContains(t, err, "start failed")
	assert.Equal(t, deps.DependencyState{Image: "postgres:18"}, st.pin)
	require.Len(t, st.commits, 1)
	assert.Empty(t, st.failures, "a finalize failure is not a migration failure")
}

func TestRunRefusesAMalformedPlan(t *testing.T) {
	ok := step("a", func(*deps.MigrationJournal) error { return nil })
	rollback := func(context.Context, *deps.MigrationJournal) error { return nil }
	result := func(j *deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{ID: j.ID} }
	cases := map[string]*Plan{
		"no steps":           {Rollback: rollback, Result: result},
		"no rollback":        {Steps: []Step{ok}, Result: result},
		"no result":          {Steps: []Step{ok}, Rollback: rollback},
		"step without a run": {Steps: []Step{{ID: "a"}}, Rollback: rollback, Result: result},
		"step without an id": {Steps: []Step{{Run: ok.Run}}, Rollback: rollback, Result: result},
	}
	for name, plan := range cases {
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{}
			err := Run(context.Background(), st, baseJournal(), plan, nil)
			require.Error(t, err)
			assert.Empty(t, st.journals, "a malformed plan must not open a journal")
			assert.Empty(t, st.commits)
			assert.Empty(t, st.failures)
		})
	}
	t.Run("no plan", func(t *testing.T) {
		require.Error(t, Run(context.Background(), &fakeStore{}, baseJournal(), nil, nil))
	})
}

func TestRollbackInterruptedRunsOnlyWithAJournal(t *testing.T) {
	st := &fakeStore{}
	called := 0
	var seen *deps.MigrationJournal
	rb := func(_ context.Context, j *deps.MigrationJournal) error {
		called++
		c := *j
		seen = &c
		return nil
	}
	ok, err := RollbackInterrupted(context.Background(), st, rb)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, called)
	assert.Empty(t, st.failures)

	j := baseJournal()
	j.Step = "dump"
	require.NoError(t, st.SetMigrationJournal(&j))
	ok, err = RollbackInterrupted(context.Background(), st, rb)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, called)
	require.NotNil(t, seen)
	assert.Equal(t, "dump", seen.Step, "rollback sees the journal as it was left")
	require.Len(t, st.failures, 1)
	assert.Contains(t, st.failures[0].Error, "interrupted")
	assert.Equal(t, deps.Postgres, st.failures[0].ID)
	assert.Empty(t, st.rbFails)
	assert.Nil(t, st.journal, "a clean rollback closes the migration")
	assert.Empty(t, st.pin, "an interrupted migration never moves the pin")
}

func TestRollbackInterruptedReportsARollbackFailure(t *testing.T) {
	st := &fakeStore{}
	j := baseJournal()
	j.CreatedVolume = "postgres3"
	require.NoError(t, st.SetMigrationJournal(&j))
	ok, err := RollbackInterrupted(context.Background(), st, func(context.Context, *deps.MigrationJournal) error {
		return errors.New("rm volume: busy")
	})
	assert.True(t, ok)
	require.ErrorContains(t, err, "rm volume: busy")
	require.Len(t, st.rbFails, 1)
	assert.Contains(t, st.rbFails[0].Error, "interrupted")
	assert.Contains(t, st.rbFails[0].Error, "rollback failed")
	assert.Empty(t, st.failures)
	require.NotNil(t, st.journal, "the journal survives so the next start retries the rollback")
	assert.Equal(t, "postgres3", st.journal.CreatedVolume)
}

// The verdict of an interrupted migration is a sentence the LAUNCHER wrote,
// not a step's own error, so it is recorded in both shapes: English on
// MigrationResult.Error, where it is persisted, logged and read back by a
// launcher that may be a different release, and as a msg.Message beside it so
// the dependency list can render it in the reader's language.
//
// Dropping the structured half is silent: Error is still there, the list still
// shows a sentence, and it is simply always English.
func TestAnInterruptedMigrationRecordsItsVerdictInBothShapes(t *testing.T) {
	rolledBack := &fakeStore{}
	j := baseJournal()
	require.NoError(t, rolledBack.SetMigrationJournal(&j))
	_, err := RollbackInterrupted(context.Background(), rolledBack,
		func(context.Context, *deps.MigrationJournal) error { return nil })
	require.NoError(t, err)
	require.Len(t, rolledBack.failures, 1)
	assert.Equal(t, "deps.msg.result.interruptedRolledBack", rolledBack.failures[0].ErrorMsg.Key)
	assert.NotEmpty(t, rolledBack.failures[0].Error, "the English half is what the state file keeps")

	failed := &fakeStore{}
	j2 := baseJournal()
	require.NoError(t, failed.SetMigrationJournal(&j2))
	_, err = RollbackInterrupted(context.Background(), failed,
		func(context.Context, *deps.MigrationJournal) error { return errors.New("rm volume: busy") })
	require.Error(t, err)
	require.Len(t, failed.rbFails, 1)
	assert.Equal(t, "deps.msg.result.interruptedRollbackFailed", failed.rbFails[0].ErrorMsg.Key)
	// The step's own error is an ARGUMENT of the sentence, never glued onto
	// the end of it: that is what lets a translator put it where their
	// language wants it.
	assert.Equal(t, []string{"error", "rm volume: busy"}, failed.rbFails[0].ErrorMsg.Args)
	assert.Contains(t, oneEN(failed.rbFails[0].ErrorMsg), "rm volume: busy")
}

func TestRollbackInterruptedRunsOnAnUncancelledContext(t *testing.T) {
	st := &fakeStore{}
	j := baseJournal()
	require.NoError(t, st.SetMigrationJournal(&j))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var rbErr error
	var hasDeadline bool
	ok, err := RollbackInterrupted(ctx, st, func(rctx context.Context, _ *deps.MigrationJournal) error {
		rbErr = rctx.Err()
		_, hasDeadline = rctx.Deadline()
		return nil
	})
	require.NoError(t, err)
	assert.True(t, ok)
	require.NoError(t, rbErr)
	assert.True(t, hasDeadline)
}

func TestRollbackInterruptedNeedsARollback(t *testing.T) {
	st := &fakeStore{}
	j := baseJournal()
	require.NoError(t, st.SetMigrationJournal(&j))
	ok, err := RollbackInterrupted(context.Background(), st, nil)
	require.Error(t, err)
	assert.False(t, ok)
	assert.NotNil(t, st.journal, "the journal survives so a later run can still undo it")
}
