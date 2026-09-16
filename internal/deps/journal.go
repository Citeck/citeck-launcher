package deps

import (
	"time"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// DependencyState is the per-dependency pin: the image the namespace's data
// last ran on successfully, and which GENERATION of the data volume that image
// runs on.
type DependencyState struct {
	Image string `json:"image"`
	// VolumeGen is which GENERATION of this dependency's data volume the pin
	// describes. It advances by ONE on every completed migration, returns to
	// PrevVolumeGen on a rollback, and is otherwise established only by
	// seeding — nothing else moves it. Absent/0 means generation 1 — the
	// volume every namespace has
	// always used — so no existing state file changes meaning, and a state
	// file written by this launcher and read by an older one still names the
	// right image.
	VolumeGen int `json:"volumeGen,omitempty"`
	// PrevImage/PrevVolumeGen are what this dependency ran on BEFORE the last
	// completed migration — the image, and the generation of the data volume
	// that migration copied FROM. They are written by CommitMigration, in the
	// same one write that moves the pin, and cleared by a rollback.
	//
	// They live on the pin and not on MigrationResult because there is one
	// result slot per NAMESPACE: migrating a second dependency would erase the
	// first one's rollback target while its retained volume was still on disk.
	//
	// An empty PrevImage means "no rollback target", which is the state of
	// every namespace that has never migrated and of every namespace that has
	// already rolled back. An old state file has neither field and reads as
	// exactly that.
	PrevImage     string `json:"prevImage,omitempty"`
	PrevVolumeGen int    `json:"prevVolumeGen,omitempty"`
}

// Gen normalizes the counter: absent, 0 and any negative are generation 1. It
// is the ONE place that rule is decided, so a state file written before the
// counter existed keeps its meaning wherever it is read.
func (s DependencyState) Gen() int {
	if s.VolumeGen < 1 {
		return 1
	}
	return s.VolumeGen
}

// Previous is the state a rollback would restore; ok=false when there is none.
//
// The generation is normalized through Gen() rather than copied raw, so a
// target recorded before the counter existed answers generation 1 even to a
// caller that reads VolumeGen directly — a rollback compares the two
// generations, and a 0 that means 1 is exactly the kind of difference that
// only shows up once the volume has been mounted.
//
// The returned state carries NO target of its own: the rollback is one step
// deep by design (see WithPrevious).
func (s DependencyState) Previous() (DependencyState, bool) {
	if s.PrevImage == "" {
		return DependencyState{}, false
	}
	prev := DependencyState{Image: s.PrevImage, VolumeGen: s.PrevVolumeGen}
	prev.VolumeGen = prev.Gen()
	return prev, true
}

// WithPrevious returns s carrying prev as its rollback target.
//
// prev's own Prev* are dropped: the rollback is ONE STEP DEEP, by design.
// Keeping the chain would mean keeping every generation's volume forever,
// which is the opposite of what the launcher tells the operator to do with the
// retained one — so 17 → 18 → 19 offers a rollback to 18 and, once taken,
// offers a rollback to nothing.
//
// A prev with no image is no target at all: a generation with nothing to run
// on would be an offer the launcher could not keep.
func (s DependencyState) WithPrevious(prev DependencyState) DependencyState {
	if prev.Image == "" {
		return s.WithoutPrevious()
	}
	s.PrevImage = prev.Image
	s.PrevVolumeGen = prev.Gen()
	return s
}

// WithoutPrevious returns s with no rollback target. A rollback CLEARS the
// target because there is no roll-forward action: re-adopting the newer volume
// would silently discard everything written since the rollback, which is the
// exact mirror of the loss the rollback's own warning is about.
func (s DependencyState) WithoutPrevious() DependencyState {
	s.PrevImage, s.PrevVolumeGen = "", 0
	return s
}

// MigrationJournal is the write-ahead record of a migration in flight. It is
// persisted after every completed step and BEFORE any creating step (a volume
// name is recorded before the volume exists), so a rollback driven only by the
// journal can always undo what was done.
type MigrationJournal struct {
	ID   ID     `json:"id"`
	From string `json:"from"` // pinned image the data runs on
	To   string `json:"to"`   // target image
	// Step is the id of the last COMPLETED step; "" before the first one.
	Step string `json:"step,omitempty"`
	// CreatedVolume is the plain name of the new data volume, recorded before
	// it is created. "" means nothing to remove.
	CreatedVolume string `json:"createdVolume,omitempty"`
	// DumpDir is the host directory holding the logical dump. A plan that
	// writes no host scratch file (the copy-upgrade plan) leaves it empty.
	DumpDir string `json:"dumpDir,omitempty"`
	// ToVolumeGen is the generation the commit moves the pin to. It is
	// recorded when the journal is OPENED, before the first step, because the
	// commit is a single write of (image, generation) and must not have to
	// re-derive the generation from a world the migration has been rewriting.
	//
	// CreatedVolume cannot stand in for it: that is the volume's NAME, and
	// parsing a generation back out of a name at commit time would make the
	// commit depend on the naming scheme staying invertible forever.
	ToVolumeGen int `json:"toVolumeGen,omitempty"`
	// SourceVolume is the volume the data came from — the one a copy-upgrade
	// plan must never write to, and the one MigrationResult.OldVolume names
	// afterwards so the operator can reclaim the space. Recorded for the same
	// reason as ToVolumeGen: after a crash the recovery has to be able to say
	// what was left where without re-deriving anything.
	SourceVolume string `json:"sourceVolume,omitempty"`
	// ScratchVolume is the ONE intermediate cluster a multi-rung PostgreSQL
	// walk reuses. "" for every single-hop migration — which is every
	// migration a launcher before this one could produce, and every namespace
	// whose bundle names a plain image rather than a ladder.
	//
	// It is a single name rather than a list because at most one intermediate
	// exists at a time: rung i is restored into it, dumped out of it, and the
	// volume is deleted before rung i+1 recreates it under the same name. The
	// peak on disk is therefore source + one cluster + one dump, whatever the
	// ladder's length. A journal written by an older launcher has no such
	// field and reads as the empty string, which is exactly what a
	// single-hop journal means today.
	ScratchVolume string `json:"scratchVolume,omitempty"`
	// WasRunning records whether the namespace was running when the migration
	// began, so both commit and rollback can start it again.
	WasRunning bool      `json:"wasRunning"`
	StartedAt  time.Time `json:"startedAt"`
}

// Result kinds for MigrationResult.Kind.
//
// Migration is the EMPTY string on purpose: every state file already written
// carries a result with no kind and stays meaningful, which is the same rule
// VolumeGen follows.
const (
	ResultKindMigration = ""
	ResultKindRollback  = "rollback"
)

// MigrationResult is the verdict of the last migration for the UI/CLI.
type MigrationResult struct {
	ID         ID        `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	FinishedAt time.Time `json:"finishedAt"`
	// Error is empty on success. It stays ENGLISH, always: it is written into
	// the namespace's state and read back later — possibly by a different
	// launcher, in a different release, with a different locale configured —
	// and it is what the slog lines and OK() are built on.
	Error string `json:"error,omitempty"`
	// ErrorMsg is the same verdict as DATA, for the verdicts the launcher
	// itself words (an interrupted migration, and its rollback). It is what the
	// dependency list renders in the reader's language; when it is Empty the
	// renderer falls back to Error, which is what every result persisted by an
	// older launcher carries and what a raw step failure — a docker error, a
	// psql stderr — legitimately has instead of a sentence.
	//
	// `omitzero`, not `omitempty`: omitempty has no effect on a struct field,
	// so every result ever persisted would carry `"errorMsg":{"Key":""}` —
	// noise in a state file a future reader has to learn to ignore. A zero
	// Message means "no structured verdict", which is exactly what omitzero
	// exists to leave out.
	ErrorMsg msg.Message `json:"errorMsg,omitzero"`
	// OldVolume names the volume the previous data was left in (success only).
	OldVolume string `json:"oldVolume,omitempty"`
	// Kind discriminates a rollback from a migration — the two share the ONE
	// result slot a namespace has, and "17.5 → 18.6 finished" and "18.6 → 17.5
	// finished" are otherwise indistinguishable to every renderer.
	//
	// It carries NO from-generation, deliberately: the rollback TARGET lives on
	// the pin (DependencyState.PrevImage/PrevVolumeGen), because one result
	// slot per namespace means a second dependency's migration would erase the
	// first one's target while its retained volume sat on disk. Recording the
	// generation in both places would be two copies of one fact that can
	// disagree.
	Kind string `json:"kind,omitempty"`
}

// OK reports success.
func (r MigrationResult) OK() bool { return r.Error == "" }
