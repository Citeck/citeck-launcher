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

// DependencyPins returns a copy of the pinned images by dependency id.
func (r *Runtime) DependencyPins() map[deps.ID]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[deps.ID]string, len(r.dependencyPins))
	for id, st := range r.dependencyPins {
		out[id] = st.Image
	}
	return out
}

// SetDependencyPin records image as the version the dependency's data runs on
// and persists immediately.
func (r *Runtime) SetDependencyPin(id deps.ID, image string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependencyPins[id] = deps.DependencyState{Image: image}
	// The error is logged by persistState; the pin is re-emitted by every
	// later persist, so a failed write here is not the caller's problem.
	_ = r.persistState()
	r.dirty.Store(false)
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
	err := r.persistState()
	r.dirty.Store(false)
	return err
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
// It is atomic IN MEMORY as well as on disk: a failed persist restores the
// three previous values. Otherwise a commit whose write failed would leave the
// runtime believing the pin had moved while the engine's failAndRollback
// removes the new volume — and the next generation would emit the new
// version's layout onto a volume that no longer exists, i.e. an empty cluster
// standing beside the intact old data.
func (r *Runtime) CommitMigration(id deps.ID, image string, res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	prevPin, hadPin := r.dependencyPins[id]
	prevJournal, prevLast := r.migrationJournal, r.lastMigration

	r.dependencyPins[id] = deps.DependencyState{Image: image}
	r.migrationJournal = nil
	r.lastMigration = cloneResult(&res)

	err := r.persistState()
	if err != nil {
		if hadPin {
			r.dependencyPins[id] = prevPin
		} else {
			delete(r.dependencyPins, id)
		}
		r.migrationJournal = prevJournal
		r.lastMigration = prevLast
		err = fmt.Errorf("commit migration of %s: %w", id, err)
	}
	r.dirty.Store(false)
	return err
}

// RecordMigrationFailure closes a rolled-back migration: journal cleared,
// verdict recorded, pin untouched.
func (r *Runtime) RecordMigrationFailure(res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrationJournal = nil
	r.lastMigration = cloneResult(&res)
	err := r.persistState()
	r.dirty.Store(false)
	return err
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
	err := r.persistState()
	r.dirty.Store(false)
	return err
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
		if r.dependencyPins[d.ID()].Image == app.Def.Image {
			continue
		}
		slog.Info("Dependency pin updated from running container", "dependency", d.ID(), "image", app.Def.Image)
		r.dependencyPins[d.ID()] = deps.DependencyState{Image: app.Def.Image}
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
