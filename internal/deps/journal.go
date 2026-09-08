package deps

import "time"

// DependencyState is the per-dependency pin: the image the namespace's data
// last ran on successfully.
type DependencyState struct {
	Image string `json:"image"`
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
	// DumpDir is the host directory holding the logical dump.
	DumpDir string `json:"dumpDir,omitempty"`
	// WasRunning records whether the namespace was running when the migration
	// began, so both commit and rollback can start it again.
	WasRunning bool      `json:"wasRunning"`
	StartedAt  time.Time `json:"startedAt"`
}

// MigrationResult is the verdict of the last migration for the UI/CLI.
type MigrationResult struct {
	ID         ID        `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	FinishedAt time.Time `json:"finishedAt"`
	// Error is empty on success.
	Error string `json:"error,omitempty"`
	// OldVolume names the volume the previous data was left in (success only).
	OldVolume string `json:"oldVolume,omitempty"`
}

// OK reports success.
func (r MigrationResult) OK() bool { return r.Error == "" }
