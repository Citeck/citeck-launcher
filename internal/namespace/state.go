package namespace

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// NsPersistedState holds runtime state that survives daemon restarts.
type NsPersistedState struct {
	Status            NsRuntimeStatus              `json:"status"`
	ManualStoppedApps []string                     `json:"manualStoppedApps,omitempty"`
	EditedApps        map[string]appdef.ApplicationDef `json:"editedApps,omitempty"`
	EditedLockedApps  []string                     `json:"editedLockedApps,omitempty"`
}

// statePath returns the path to the persisted state file (namespace-scoped).
func statePath(volumesBase, nsID string) string {
	return filepath.Join(volumesBase, "state-"+nsID+".json")
}

// SaveNsState persists the namespace runtime state to disk.
func SaveNsState(volumesBase, nsID string, state *NsPersistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := statePath(volumesBase, nsID)
	os.MkdirAll(filepath.Dir(path), 0o755)
	// Atomic write: write to temp file then rename (POSIX atomic)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadNsState reads the persisted namespace state from disk.
// Returns nil if no state file exists.
func LoadNsState(volumesBase, nsID string) *NsPersistedState {
	path := statePath(volumesBase, nsID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state NsPersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}
