package namespace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// NsPersistedState holds runtime state that survives daemon restarts.
type NsPersistedState struct {
	Status            NsRuntimeStatus                  `json:"status"`
	ManualStoppedApps []string                         `json:"manualStoppedApps,omitempty"`
	EditedApps        map[string]appdef.ApplicationDef `json:"editedApps,omitempty"`
	EditedLockedApps  []string                         `json:"editedLockedApps,omitempty"`
	CachedBundle      *bundle.Def                      `json:"cachedBundle,omitempty"`
}

// statePath returns the path to the persisted state file (namespace-scoped).
func statePath(volumesBase, nsID string) string {
	return filepath.Join(volumesBase, "state-"+nsID+".json")
}

// SaveNsState persists the namespace runtime state to disk.
func SaveNsState(volumesBase, nsID string, state *NsPersistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	path := statePath(volumesBase, nsID)
	_ = os.MkdirAll(filepath.Dir(path), 0o750)
	if err := fsutil.AtomicWriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
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
