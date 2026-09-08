// Package migrate runs a dependency migration plan step by step with a
// write-ahead journal and a journal-driven rollback.
//
// Contract:
//   - the journal is persisted before the first step and after every step;
//   - a step that creates something records it in the journal and calls
//     Journal.Persist BEFORE creating it, so a crash between the two cannot
//     orphan it — rollback is driven only by what the journal says exists;
//   - any step error, a journal write that fails, or a context cancellation
//     runs Plan.Rollback on a context that is NOT canceled (bounded by
//     RollbackTimeout) and records a failure; the pin never moves;
//   - after the last step the store's CommitMigration is the single write that
//     moves the pin, clears the journal and records the result; Plan.Finalize
//     then does the irreversible tidy (delete scratch, restart the namespace),
//     whose failure is reported as *FinalizeError, never rolled back.
//
// Precondition the engine relies on and does not enforce: a plan's first step
// stops the namespace, and the whole run holds the daemon's long-operation
// lock, so the commit happens while the namespace is STOPPED. That is what
// keeps the runtime loop from observing the pre-migration container after
// CommitMigration and re-pinning the old image over the new one (the Runtime's
// RUNNING re-pin hook also stands aside while a journal is open).
package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// RollbackTimeout bounds a rollback that runs after the triggering context
// was canceled (daemon shutdown).
const RollbackTimeout = 10 * time.Minute

// JournalStore persists the journal and the verdict. The namespace Runtime
// implements it.
type JournalStore interface {
	MigrationJournal() *deps.MigrationJournal
	SetMigrationJournal(j *deps.MigrationJournal) error
	CommitMigration(id deps.ID, image string, res deps.MigrationResult) error
	RecordMigrationFailure(res deps.MigrationResult) error
}

// StepProgress reports sub-progress of the running step (percent 0..100, or
// 0 for indeterminate) with a human message.
type StepProgress func(percent float64, message string)

// Progress reports engine-level progress: which step (1-based index of total)
// and the step's latest sub-progress.
type Progress func(stepID string, index, total int, percent float64, message string)

// Step is one reversible unit of work. Run may mutate the journal; the engine
// persists it after Run returns. A step that must persist BEFORE acting calls
// j.Persist (see Journal).
type Step struct {
	ID  string
	Run func(ctx context.Context, j *Journal, p StepProgress) error
}

// Journal wraps the persisted record with a Persist hook so a step can make
// a write-ahead entry (record a volume name, then create the volume). Steps
// take this type rather than deps.MigrationJournal precisely because of that
// hook; Plan's own callbacks, which never write ahead, take the plain record.
type Journal struct {
	deps.MigrationJournal
	persist func(deps.MigrationJournal) error
}

// Persist writes the journal now.
func (j *Journal) Persist() error { return j.persist(j.MigrationJournal) }

// Plan is what a descriptor's migrator produces for one (from → to) move.
type Plan struct {
	Steps    []Step
	Rollback func(ctx context.Context, j *deps.MigrationJournal) error
	Result   func(j *deps.MigrationJournal) deps.MigrationResult
	Finalize func(ctx context.Context, j *deps.MigrationJournal) error
}

// validate refuses a plan the engine could only handle by panicking. It runs
// before the journal is opened, so a malformed plan changes nothing.
func (p *Plan) validate() error {
	if p == nil {
		return errors.New("migration plan is nil")
	}
	if len(p.Steps) == 0 {
		return errors.New("migration plan has no steps")
	}
	if p.Rollback == nil {
		return errors.New("migration plan has no rollback")
	}
	if p.Result == nil {
		return errors.New("migration plan has no result")
	}
	for i, st := range p.Steps {
		if st.ID == "" {
			return fmt.Errorf("migration plan step %d has no id", i+1)
		}
		if st.Run == nil {
			return fmt.Errorf("migration plan step %s has no run function", st.ID)
		}
	}
	return nil
}

// FinalizeError means the migration committed but the post-commit tidy or
// restart failed; the caller reports it as a warning, not a failure.
type FinalizeError struct{ Err error }

func (e *FinalizeError) Error() string {
	return "migration committed; finalize failed: " + e.Err.Error()
}
func (e *FinalizeError) Unwrap() error { return e.Err }

// Run executes plan for journal j.
func Run(ctx context.Context, store JournalStore, j deps.MigrationJournal, plan *Plan, progress Progress) error {
	if err := plan.validate(); err != nil {
		return err
	}
	if progress == nil {
		progress = func(string, int, int, float64, string) {}
	}
	// The store is handed a COPY (rec is a value parameter), so it can never
	// alias the journal the steps keep mutating.
	persist := func(rec deps.MigrationJournal) error { return store.SetMigrationJournal(&rec) }
	jj := &Journal{MigrationJournal: j, persist: persist}
	if err := jj.Persist(); err != nil {
		// Nothing has been done yet, so there is nothing to roll back — and
		// no journal to drive one with.
		return fmt.Errorf("persist migration journal: %w", err)
	}
	total := len(plan.Steps)
	for i, st := range plan.Steps {
		if err := ctx.Err(); err != nil {
			return failAndRollback(ctx, store, plan, jj, fmt.Errorf("canceled before step %s: %w", st.ID, err))
		}
		progress(st.ID, i+1, total, 0, "")
		sub := func(pct float64, msg string) { progress(st.ID, i+1, total, pct, msg) }
		slog.Info("Dependency migration step", "dependency", jj.ID, "step", st.ID, "index", i+1, "total", total)
		if err := st.Run(ctx, jj, sub); err != nil {
			return failAndRollback(ctx, store, plan, jj, fmt.Errorf("step %s: %w", st.ID, err))
		}
		jj.Step = st.ID
		if err := jj.Persist(); err != nil {
			return failAndRollback(ctx, store, plan, jj, fmt.Errorf("persist journal after %s: %w", st.ID, err))
		}
	}
	// The commit is the point of no return, so a cancellation that landed
	// during the last step still rolls back instead of moving the pin.
	if err := ctx.Err(); err != nil {
		return failAndRollback(ctx, store, plan, jj, fmt.Errorf("canceled before commit: %w", err))
	}
	res := plan.Result(&jj.MigrationJournal)
	// The journal is the single source of truth for what moved where: the pin,
	// the journal id and the result's identity all come from it, whatever the
	// plan's Result left blank.
	res.ID, res.From, res.To = jj.ID, jj.From, jj.To
	res.FinishedAt = time.Now()
	if err := store.CommitMigration(jj.ID, jj.To, res); err != nil {
		return failAndRollback(ctx, store, plan, jj, fmt.Errorf("commit: %w", err))
	}
	slog.Info("Dependency migration committed", "dependency", jj.ID, "from", jj.From, "to", jj.To)
	if plan.Finalize != nil {
		if err := plan.Finalize(ctx, &jj.MigrationJournal); err != nil {
			slog.Warn("Dependency migration finalize failed", "dependency", jj.ID, "err", err)
			return &FinalizeError{Err: err}
		}
	}
	return nil
}

func failAndRollback(ctx context.Context, store JournalStore, plan *Plan, jj *Journal, cause error) error {
	slog.Warn("Dependency migration failed; rolling back", "dependency", jj.ID, "step", jj.Step, "err", cause)
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RollbackTimeout)
	defer cancel()
	res := deps.MigrationResult{ID: jj.ID, From: jj.From, To: jj.To, FinishedAt: time.Now(), Error: cause.Error()}
	rbErr := plan.Rollback(rbCtx, &jj.MigrationJournal)
	if rbErr != nil {
		slog.Error("Dependency migration rollback failed", "dependency", jj.ID, "err", rbErr)
		res.Error += "; rollback failed: " + rbErr.Error()
	}
	if err := store.RecordMigrationFailure(res); err != nil {
		slog.Error("Failed to record migration failure", "dependency", jj.ID, "err", err)
	}
	if rbErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback failed: %w", rbErr))
	}
	return cause
}

// RollbackInterrupted undoes a migration whose journal survived a daemon
// restart. rolledBack=false when there was nothing to do.
func RollbackInterrupted(ctx context.Context, store JournalStore, rollback func(context.Context, *deps.MigrationJournal) error) (bool, error) {
	j := store.MigrationJournal()
	if j == nil {
		return false, nil
	}
	if rollback == nil {
		// Leave the journal alone: a launcher that cannot undo this migration
		// must not erase the only record of what it left behind.
		return false, fmt.Errorf("interrupted migration of %s: no rollback available", j.ID)
	}
	slog.Warn("Interrupted dependency migration found; rolling back", "dependency", j.ID, "step", j.Step)
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RollbackTimeout)
	defer cancel()
	res := deps.MigrationResult{
		ID: j.ID, From: j.From, To: j.To, FinishedAt: time.Now(),
		Error: "migration interrupted by a launcher restart; rolled back",
	}
	rbErr := rollback(rbCtx, j)
	if rbErr != nil {
		slog.Error("Rollback of an interrupted dependency migration failed", "dependency", j.ID, "err", rbErr)
		res.Error += "; rollback failed: " + rbErr.Error()
		rbErr = fmt.Errorf("rollback failed: %w", rbErr)
	}
	if err := store.RecordMigrationFailure(res); err != nil {
		return true, errors.Join(rbErr, fmt.Errorf("record migration failure: %w", err))
	}
	return true, rbErr
}
