package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// State is the lifecycle of a staged daemon payload.
type State string

const (
	StateStaged  State = "staged"  // downloaded + verified, not yet applied
	StatePending State = "pending" // user applied; under health-gate this boot
	StateGood    State = "good"    // health-gate passed; selectable as daemon
	StateFailed  State = "failed"  // health-gate failed; never selected again
)

// Entry is one staged payload in the manifest.
type Entry struct {
	Version  string `json:"version"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256,omitempty"`
	State    State  `json:"state"`
	StagedAt string `json:"stagedAt,omitempty"`
	HealthAt string `json:"healthAt,omitempty"`
}

// Manifest is the on-disk inventory of staged payloads (updates/manifest.json).
type Manifest struct {
	Entries []Entry `json:"entries"`
}

func manifestPath(updatesDir string) string {
	return filepath.Join(updatesDir, "manifest.json")
}

// Load reads the manifest. A missing file yields an empty manifest and nil error.
func Load(updatesDir string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath(updatesDir)) //nolint:gosec // G304: path from trusted config
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{}, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// Save atomically writes the manifest, creating updatesDir if needed.
func Save(updatesDir string, m *Manifest) error {
	if err := os.MkdirAll(updatesDir, 0o755); err != nil { //nolint:gosec // updates dir needs 0o755
		return fmt.Errorf("mkdir updates: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := fsutil.AtomicWriteFile(manifestPath(updatesDir), data, 0o644); err != nil { //nolint:gosec // not sensitive
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// AddStaged upserts an entry in the StateStaged state (overwriting any prior
// entry for the same version).
func AddStaged(updatesDir string, e Entry) error {
	m, err := Load(updatesDir)
	if err != nil {
		return err
	}
	e.State = StateStaged
	e.StagedAt = time.Now().UTC().Format(time.RFC3339)
	out := m.Entries[:0:0]
	for _, ex := range m.Entries {
		if ex.Version != e.Version {
			out = append(out, ex)
		}
	}
	out = append(out, e)
	m.Entries = out
	return Save(updatesDir, m)
}

// MarkState transitions the entry for version to the given state.
func MarkState(updatesDir, version string, state State) error {
	m, err := Load(updatesDir)
	if err != nil {
		return err
	}
	found := false
	for i := range m.Entries {
		if m.Entries[i].Version == version {
			m.Entries[i].State = state
			m.Entries[i].HealthAt = time.Now().UTC().Format(time.RFC3339)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("manifest: no entry for version %s", version)
	}
	return Save(updatesDir, m)
}

// SelectBest returns the path of the newest payload that is (a) State good or
// pending, (b) strictly newer than currentVersion (never-downgrade), and (c)
// present on disk. ok=false when none qualifies — the caller falls back to the
// bundled executable.
func SelectBest(updatesDir, currentVersion string) (path string, ok bool) {
	m, err := Load(updatesDir)
	if err != nil {
		return "", false
	}
	best := ""
	for _, e := range m.Entries {
		if e.State != StateGood && e.State != StatePending {
			continue
		}
		if !Greater(e.Version, currentVersion) {
			continue
		}
		if _, statErr := os.Stat(e.Path); statErr != nil {
			continue
		}
		if best == "" || Greater(e.Version, best) {
			best, path = e.Version, e.Path
		}
	}
	return path, best != ""
}

// FailedNewerThan returns the version of a failed payload newer than current, if
// any — used by Status to report "update failed, rolled back".
func FailedNewerThan(updatesDir, currentVersion string) string {
	m, err := Load(updatesDir)
	if err != nil {
		return ""
	}
	worst := ""
	for _, e := range m.Entries {
		if e.State == StateFailed && Greater(e.Version, currentVersion) {
			if worst == "" || Greater(e.Version, worst) {
				worst = e.Version
			}
		}
	}
	return worst
}
