package namespace

import (
	"fmt"
	"log/slog"
	"maps"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// Dependency state (pins, migration journal, last result) is Runtime-owned for
// one reason: persistState rebuilds NsPersistedState from Runtime fields and
// overwrites the whole record, so anything the migration engine wrote into the
// JSON directly would be erased by the next unrelated persist. Every mutation
// goes through these methods and persists inline (durable intent, like
// StopApp / UpdateAppDef).

// DependencyStates returns a copy of the whole pin record by dependency id:
// the image the data last ran on AND which generation of the data volume it
// lives in. It is what the GENERATOR reads — both halves name something it has
// to emit — while DependencyPins below is the image-only view for callers that
// only ask about versions.
func (r *Runtime) DependencyStates() map[deps.ID]deps.DependencyState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[deps.ID]deps.DependencyState, len(r.dependencyPins))
	maps.Copy(out, r.dependencyPins)
	return out
}

// DependencyPins returns a copy of the pinned images by dependency id: the
// version half of the record, for the edit gate and the dependency list, which
// ask what a dependency RUNS and have no business with its volume.
func (r *Runtime) DependencyPins() map[deps.ID]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[deps.ID]string, len(r.dependencyPins))
	for id, st := range r.dependencyPins {
		out[id] = st.Image
	}
	return out
}

// SetDependencyState records the whole pin — image and volume generation — as
// what the dependency's data runs on, and persists immediately.
//
// It takes the state WHOLE rather than an image, because the two halves are
// one fact: seeding derives both from the same evidence, and a caller that
// could set the image alone would silently reset the generation to 1 and mount
// the pre-migration volume on the next start.
func (r *Runtime) SetDependencyState(id deps.ID, st deps.DependencyState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependencyPins[id] = st
	// The caller has nothing to do with a failed write (there is no pin to
	// fall back to), so the error is not returned — but it is not forgotten
	// either: persistUnderLock leaves the write owed.
	_ = r.persistUnderLock("set-pin")
}

// RestoreDependencyState installs the persisted dependency state (called
// before first start, like RestoreRestartState). No persist: the load path
// runs before the caller has acted on ShouldStart, and persisting there would
// write r.status while it is still STOPPED.
//
// It REPLACES all three fields, so a nil journal or a nil last result CLEARS
// whatever the runtime held — which is what a restore must do (the state file
// is the truth), and what makes passing a partially-filled state a way to
// silently drop an open journal.
func (r *Runtime) RestoreDependencyState(pins map[deps.ID]deps.DependencyState, journal *deps.MigrationJournal, last *deps.MigrationResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependencyPins = make(map[deps.ID]deps.DependencyState, len(pins))
	maps.Copy(r.dependencyPins, pins)
	r.migrationJournal = cloneJournal(journal)
	r.lastMigration = cloneResult(last)
}

// MigrationJournal returns a copy of the in-flight journal, or nil.
func (r *Runtime) MigrationJournal() *deps.MigrationJournal {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneJournal(r.migrationJournal)
}

// SetMigrationJournal replaces the journal (nil clears it) and persists.
func (r *Runtime) SetMigrationJournal(j *deps.MigrationJournal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrationJournal = cloneJournal(j)
	return r.persistUnderLock("set-journal")
}

// LastDependencyMigration returns a copy of the last verdict, or nil.
func (r *Runtime) LastDependencyMigration() *deps.MigrationResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneResult(r.lastMigration)
}

// CommitMigration is the single write that makes a migration real: the pin
// moves to the new image, the journal is cleared and the verdict recorded,
// all in one persist so no crash can leave a pin without a cleared journal
// or the other way round.
//
// The caller does not choose the identity: the engine calls this with id and
// res.ID taken from the same journal, so a commit can never file one
// dependency's verdict under another's pin. (Nothing here re-checks it —
// there is one caller, migrate.Run's commit, and it is the journal that
// decides both.)
//
// The state moves WHOLE: the image AND the volume generation the migrated data
// now lives in. They are one fact — the new image reads the volume the copy
// landed in — and a commit that moved only the image would leave the next
// generation mounting the source volume the migration deliberately never wrote
// to.
//
// It is atomic IN MEMORY as well as on disk: a failed persist restores the
// three previous values. Otherwise a commit whose write failed would leave the
// runtime believing the pin had moved while the engine's failAndRollback
// removes the new volume — and the next generation would emit the new
// version's layout onto a volume that no longer exists, i.e. an empty cluster
// standing beside the intact old data.
//
// The state it moved FROM is recorded in the same write, as the pin's rollback
// target: it is what "put this dependency back where it was" restores, and it
// belongs on the PIN rather than on the result because a namespace has exactly
// one result slot — migrating a second dependency would otherwise erase the
// first one's target while its retained volume was still on disk. A first-ever
// migration of a dependency that had no pin records no target (there is no
// image to go back to), which deps.DependencyState.WithPrevious decides.
func (r *Runtime) CommitMigration(id deps.ID, st deps.DependencyState, res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.closeMigrationUnderLock(id, st.WithPrevious(r.dependencyPins[id]), res, "commit-migration"); err != nil {
		return fmt.Errorf("commit migration of %s: %w", id, err)
	}
	return nil
}

// RollbackDependencyState is the mirror of CommitMigration: the pin goes back
// to the state a completed migration moved away from — the image AND the
// generation of the volume that migration copied FROM, which is the volume the
// namespace is about to run on again — the journal is cleared and the verdict
// recorded, in the same single write and with the same in-memory atomicity.
//
// It WITHDRAWS the rollback offer itself rather than trusting the caller to
// pass a cleared state, because leaving it standing would be an offer the
// launcher cannot keep: there is no roll-forward action, so re-adopting the
// newer volume later would silently discard everything written since the
// rollback — the exact mirror of the loss the rollback's own warning is about.
func (r *Runtime) RollbackDependencyState(id deps.ID, st deps.DependencyState, res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.closeMigrationUnderLock(id, st.WithoutPrevious(), res, "rollback-dependency"); err != nil {
		return fmt.Errorf("roll back %s: %w", id, err)
	}
	return nil
}

// closeMigrationUnderLock is the one write that closes a migration OR a
// rollback: the pin becomes st, the journal is cleared and the verdict is
// recorded, in a single persist so no crash can leave a moved pin beside an
// open journal or the other way round.
//
// On a refused write all three are put back. Both callers treat the error as
// "the move did not happen" and undo what they built (the engine removes the
// volume it created; a rollback leaves the namespace on the newer generation),
// so a runtime that already believed the move had landed would generate a def
// for a volume that is being taken away.
//
// Caller must hold r.mu.Lock.
func (r *Runtime) closeMigrationUnderLock(id deps.ID, st deps.DependencyState, res deps.MigrationResult, mutation string) error {
	prevPin, hadPin := r.dependencyPins[id]
	prevJournal, prevLast := r.migrationJournal, r.lastMigration

	r.dependencyPins[id] = st
	r.migrationJournal = nil
	r.lastMigration = cloneResult(&res)

	err := r.persistUnderLock(mutation)
	if err != nil {
		if hadPin {
			r.dependencyPins[id] = prevPin
		} else {
			delete(r.dependencyPins, id)
		}
		r.migrationJournal = prevJournal
		r.lastMigration = prevLast
	}
	return err
}

// RecordMigrationFailure closes a rolled-back migration: journal cleared,
// verdict recorded, pin untouched.
func (r *Runtime) RecordMigrationFailure(res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrationJournal = nil
	r.lastMigration = cloneResult(&res)
	return r.persistUnderLock("record-migration-failure")
}

// RecordRollbackFailure closes nothing: it records the verdict of a migration
// whose ROLLBACK failed and deliberately LEAVES THE JOURNAL in place. What the
// journal describes — the half-created target volume, the temp containers — is
// still on the host, so clearing it would strand those leftovers with no record
// and no way for a later start to retry. One write, like every other mutation
// here.
func (r *Runtime) RecordRollbackFailure(res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastMigration = cloneResult(&res)
	return r.persistUnderLock("record-rollback-failure")
}

// syncDependencyPinsUnderLock re-pins every registered dependency whose app
// is RUNNING on an image different from its pin. Running is the proof the
// data accepted that version, so this is where a non-breaking bump (17.5 →
// 17.11) becomes the new baseline. One place covers both RUNNING entries —
// a fresh start (commitRunningUnderLock) and container adoption in doStart —
// which is why it runs in the loop tail rather than in either of them.
//
// It marks r.dirty instead of persisting: the loop tail coalesces the write
// with everything else the iteration changed.
//
// This is the spec's pin writer #1, and it coexists with CommitMigration
// (writer #2) on one precondition: a migration commits its pin only while the
// namespace is STOPPED, under the long-operation lock, so the loop is not
// running and can never observe the pre-migration container after
// CommitMigration and undo it. The journal guard below is defense in depth for
// that precondition — while a migration is open its dependency's pin belongs
// to the migration, whatever happens to be running mid-flight.
//
// Caller must hold r.mu.Lock.
func (r *Runtime) syncDependencyPinsUnderLock() {
	if r.migrationJournal != nil {
		return
	}
	for _, d := range deps.All() {
		app, ok := r.apps[d.AppName()]
		// An empty image says nothing about what the data runs on; blanking
		// the pin would lose the only record of the version the volume was
		// created by.
		if !ok || app.Status != AppStatusRunning || app.Def.Image == "" {
			continue
		}
		pin := r.dependencyPins[d.ID()]
		if pin.Image == app.Def.Image {
			continue
		}
		slog.Info("Dependency pin updated from running container",
			"namespace", r.nsID, "dependency", d.ID(), "image", app.Def.Image)
		// The IMAGE follows the container; the GENERATION does not. What is
		// running is proof of the version the data accepted, and proof of
		// nothing about which volume it is in — only a completed migration
		// moves the data, so only CommitMigration moves the counter. Writing a
		// fresh DependencyState here would reset it to 1 and mount the
		// pre-migration volume on the next start.
		//
		// The ROLLBACK TARGET is preserved for the same reason and one more:
		// the volume it names is still on disk, so the offer is still good, and
		// a patch bump of the version now running says nothing about the one
		// the namespace could go back to. Only a rollback withdraws it.
		pin.Image = app.Def.Image
		r.dependencyPins[d.ID()] = pin
		r.dirty.Store(true)
	}
}

func cloneJournal(j *deps.MigrationJournal) *deps.MigrationJournal {
	if j == nil {
		return nil
	}
	c := *j
	return &c
}

func cloneResult(res *deps.MigrationResult) *deps.MigrationResult {
	if res == nil {
		return nil
	}
	c := *res
	return &c
}
