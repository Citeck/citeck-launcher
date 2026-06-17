package namespace

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// NsPersistedState holds runtime state that survives daemon restarts.
type NsPersistedState struct {
	Status            NsRuntimeStatus `json:"status"`
	ManualStoppedApps []string        `json:"manualStoppedApps,omitempty"`

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
