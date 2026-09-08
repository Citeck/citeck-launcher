package namespace

import (
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
// before first start, like RestoreRestartState). No persist.
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
func (r *Runtime) CommitMigration(id deps.ID, image string, res deps.MigrationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dependencyPins[id] = deps.DependencyState{Image: image}
	r.migrationJournal = nil
	r.lastMigration = cloneResult(&res)
	err := r.persistState()
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
