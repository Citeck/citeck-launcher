package namespace

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// NsPersistedState holds runtime state that survives daemon restarts.
type NsPersistedState struct {
	Status            NsRuntimeStatus `json:"status"`
	ManualStoppedApps []string        `json:"manualStoppedApps,omitempty"`

	// KnownApps is every app id this namespace has already produced at least
	// once. Its only job is to tell an app that arrives with a new bundle
	// release from one the operator has been running all along: a NEW app whose
	// workspace template lists it in detachedApps starts detached instead of
	// starting itself. Absent means the state predates the field, and
	// namespace.BaselineKnownApps() stands in for it — see DecideNewAppDetach.
	KnownApps []string `json:"knownApps,omitempty"`

	// Delta model (2.7+). EditedAppPatches[name] is a JSON merge patch over the
	// generated ApplicationDef; EditedFileEdits[key] is a per-file delta
	// (key = "<app>/<rel>", no leading "./").
	EditedAppPatches map[string]json.RawMessage `json:"editedAppPatches,omitempty"`
	EditedFileEdits  map[string]FileEdit        `json:"editedFileEdits,omitempty"`

	// Legacy full-replacement model (≤2.6). Read on load for one-shot migration;
	// never written by persistState anymore.
	EditedApps       map[string]appdef.ApplicationDef `json:"editedApps,omitempty"`
	EditedLockedApps []string                         `json:"editedLockedApps,omitempty"`
	EditedFiles      []string                         `json:"editedFiles,omitempty"`

	CachedBundle  *bundle.Def    `json:"cachedBundle,omitempty"`
	RestartEvents []RestartEvent `json:"restartEvents,omitempty"`
	RestartCounts map[string]int `json:"restartCounts,omitempty"`

	// Dependencies pins, per infra dependency, the image the namespace's data
	// last ran on successfully. The generator holds a bundle's newer image back
	// when moving to it would be a breaking change (see internal/deps), so a
	// bundle bump never silently recreates postgres onto a data directory it
	// cannot read. Absent for a namespace that has no data yet.
	Dependencies map[deps.ID]deps.DependencyState `json:"dependencies,omitempty"`
	// DependencyMigration is the write-ahead journal of a migration in flight;
	// non-nil on load means the daemon died mid-migration and must roll back.
	DependencyMigration *deps.MigrationJournal `json:"dependencyMigration,omitempty"`
	// LastDependencyMigration is the verdict of the most recent migration.
	LastDependencyMigration *deps.MigrationResult `json:"lastDependencyMigration,omitempty"`

	// ContentStorageSwitchPending is the ECOS storage ref the daemon has
	// started connecting ecos-content to but has not yet seen ECOS confirm as
	// the default: written before the first record is created, cleared once
	// the default reads back as that storage (or ECOS shows an administrator's
	// own choice). It is NOT a "done" flag — the verdict is still derived from
	// ECOS on every run. It only tells the launcher's own unfinished switch
	// ("our storage exists, the default is still LOCAL") apart from an
	// administrator who switched back on purpose, which ECOS alone cannot.
	ContentStorageSwitchPending string `json:"contentStorageSwitchPending,omitempty"`
}

// statePath returns the path to the persisted state file (namespace-scoped).
func statePath(volumesBase, nsID string) string {
	return filepath.Join(volumesBase, "state-"+nsID+".json")
}

// LoadNsState reads the persisted namespace state from disk.
// Returns nil if no state file exists.
func LoadNsState(volumesBase, nsID string) *NsPersistedState {
	path := statePath(volumesBase, nsID)
	data, err := os.ReadFile(path) //nolint:gosec // path is from trusted volumesBase + nsID
	if err != nil {
		return nil
	}
	var state NsPersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}
