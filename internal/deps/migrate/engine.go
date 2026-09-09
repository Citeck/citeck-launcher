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
//   - a rollback that SUCCEEDS clears the journal, a rollback that FAILS keeps
//     it: what the journal describes (the new volume, the temp containers) is
//     still out there, so the next start must find the record and retry the
//     rollback rather than inherit untracked leftovers;
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
	// CommitMigration moves the pin to image, clears the journal and records
	// res. The engine REQUIRES those three to be one write, and to be atomic in
	// memory as well as on disk: an implementation that moved the pin and then
	// failed to persist would have the runtime believe the data is on the new
	// volume while this engine's failAndRollback deletes it — leaving a brand
	// new empty cluster standing beside the intact old data. A failed commit
	// must therefore leave the pin, the journal and the last result exactly as
	// they were, and say so by returning an error.
	CommitMigration(id deps.ID, image string, res deps.MigrationResult) error
	// RecordMigrationFailure closes a migration that was fully rolled back:
	// the verdict is recorded and the journal cleared.
	//
	// That it does NOT mirror RecordRollbackFailure below is the point, not an
	// oversight to tidy up: the two differ in exactly one thing — whether the
	// journal survives — and recordVerdict is the single place that chooses
	// between them, on the rollback's own outcome. Collapsing them into one
	// method with a flag, or making both clear the journal, erases the
	// difference between a closed migration and leftovers (a half-built volume,
	// temp containers holding the namespace's data volume) that the next start
	// must still find and clean up.
	RecordMigrationFailure(res deps.MigrationResult) error
	// RecordRollbackFailure records the verdict of a migration whose ROLLBACK
	// failed and leaves the journal in place, so the next start retries it.
	RecordRollbackFailure(res deps.MigrationResult) error
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
	if err := recordVerdict(store, res, rbErr); err != nil {
		slog.Error("Failed to record migration failure", "dependency", jj.ID, "err", err)
	}
	if rbErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback failed: %w", rbErr))
	}
	return cause
}

// recordVerdict is the one place that decides what happens to the journal: a
// clean rollback closes the migration, a failed one keeps the journal so the
// next start can retry it against the leftovers it still describes.
func recordVerdict(store JournalStore, res deps.MigrationResult, rbErr error) error {
	if rbErr != nil {
		if err := store.RecordRollbackFailure(res); err != nil {
			return fmt.Errorf("record rollback failure: %w", err)
		}
		return nil
	}
	if err := store.RecordMigrationFailure(res); err != nil {
		return fmt.Errorf("record migration failure: %w", err)
	}
	return nil
}

// RollbackInterrupted undoes a migration whose journal survived a daemon
// restart.
//
// rolledBack=false means NOTHING WAS ATTEMPTED, which happens two ways and
// they are not the same: there was no journal (err nil — the ordinary boot),
// or there is one and this launcher has no rollback for it (err non-nil, and
// the journal is deliberately left untouched: a launcher that cannot undo a
// migration must not erase the only record of what it left behind).
//
// rolledBack=true means the rollback was ATTEMPTED, not that it restored
// anything: on a failed rollback it is true AND err is non-nil, with the
// journal still open.
//
// A rollback that succeeds clears the journal; one that fails keeps it (with
// the verdict recorded), so the next start finds the record and tries again —
// the alternative is a target volume and temp containers nobody knows about.
//
// So err, never rolledBack, is what a caller may key a namespace restart on:
// true-with-an-error is precisely the state in which the temp containers may
// still hold the namespace's own data volume, and starting the namespace over
// them would put a second postmaster on the user's only copy of the data.
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
	res := deps.MigrationResult{ID: j.ID, From: j.From, To: j.To, FinishedAt: time.Now()}
	rbErr := rollback(rbCtx, j)
	if rbErr != nil {
		slog.Error("Rollback of an interrupted dependency migration failed", "dependency", j.ID, "err", rbErr)
		// Do not claim it was rolled back when it was not: the journal stays,
		// and the verdict has to say why.
		res.Error = "migration interrupted by a launcher restart; rollback failed: " + rbErr.Error()
		rbErr = fmt.Errorf("rollback failed: %w", rbErr)
	} else {
		res.Error = "migration interrupted by a launcher restart; rolled back"
	}
	if err := recordVerdict(store, res, rbErr); err != nil {
		return true, errors.Join(rbErr, err)
	}
	return true, rbErr
}
