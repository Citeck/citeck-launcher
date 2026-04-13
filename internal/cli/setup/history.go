package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// PatchRecord captures a single configuration change with forward and reverse patches.
type PatchRecord struct {
	Date      time.Time      `json:"date"`
	Setting   string         `json:"setting"`
	Command   string         `json:"command"`
	Input     map[string]any `json:"input"`
	Forward   []PatchOp      `json:"forward"`
	Reverse   []PatchOp      `json:"reverse"`
	SecretOps *SecretOps     `json:"secretOps,omitempty"`
}

// SecretOps holds secret-level forward and reverse operations paired with a patch.
type SecretOps struct {
	Forward []SecretOp `json:"forward"`
	Reverse []SecretOp `json:"reverse"`
}

// SecretOp describes a single secret mutation (set or delete).
type SecretOp struct {
	Key     string `json:"key"`
	Backup  string `json:"backup,omitempty"`
	Restore string `json:"restore,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

const snapshotFileName = "snapshot.json"

// historyDir derives the history directory path from a config file path.
// /conf/namespace.yml → /conf/namespace_history
// /conf/daemon.yml    → /conf/daemon_history
// /a/b/myconfig.yaml  → /a/b/myconfig_history
func historyDir(configPath string) string {
	base := filepath.Base(configPath)
	dir := filepath.Dir(configPath)
	// Strip extension (.yml or .yaml).
	name := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
	return filepath.Join(dir, name+"_history")
}

// patchFileName returns a sortable filename for a patch record.
// Format: 2026-04-08T12-30-00.123_hostname.json
func patchFileName(t time.Time, setting string) string {
	ms := t.UnixMilli() % 1000
	return fmt.Sprintf("%sT%s.%03d_%s.json",
		t.UTC().Format("2006-01-02"),
		t.UTC().Format("15-04-05"),
		ms,
		setting,
	)
}

// writePatch serializes patch to a JSON file in histDir and returns the path.
func writePatch(histDir string, patch *PatchRecord) (string, error) {
	if err := os.MkdirAll(histDir, 0o750); err != nil {
		return "", fmt.Errorf("create history dir: %w", err)
	}
	name := patchFileName(patch.Date, patch.Setting)
	path := filepath.Join(histDir, name)

	data, err := json.MarshalIndent(patch, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal patch: %w", err)
	}
	if wErr := fsutil.AtomicWriteFile(path, data, 0o644); wErr != nil {
		return "", fmt.Errorf("write patch file: %w", wErr)
	}
	return path, nil
}

// readPatch reads and unmarshals a patch record from path.
func readPatch(path string) (*PatchRecord, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is computed internally from history dir, not user-supplied
	if err != nil {
		return nil, fmt.Errorf("read patch %s: %w", path, err)
	}
	var rec PatchRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal patch %s: %w", path, err)
	}
	return &rec, nil
}

// writeSnapshot persists the current config content as a snapshot in histDir.
func writeSnapshot(histDir string, data []byte) error {
	if err := os.MkdirAll(histDir, 0o750); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	path := filepath.Join(histDir, snapshotFileName)
	if wErr := fsutil.AtomicWriteFile(path, data, 0o644); wErr != nil {
		return fmt.Errorf("write snapshot file: %w", wErr)
	}
	return nil
}

// readSnapshot reads the snapshot from histDir. Returns nil, nil if not found.
func readSnapshot(histDir string) ([]byte, error) {
	path := filepath.Join(histDir, snapshotFileName)
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is computed internally from history dir, not user-supplied
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	return data, nil
}

// listPatches returns all patch records in histDir sorted by date (oldest first).
// Returns nil (not an error) when histDir does not exist.
func listPatches(histDir string) ([]*PatchRecord, error) {
	entries, err := os.ReadDir(histDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history dir: %w", err)
	}

	records := make([]*PatchRecord, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == snapshotFileName {
			continue
		}
		rec, err := readPatch(filepath.Join(histDir, e.Name()))
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Date.Before(records[j].Date)
	})
	return records, nil
}

// checkBridge compares currentData against the saved snapshot.
// On first run (no snapshot) it writes the snapshot and returns false.
// If the data has changed externally (not by a recorded patch) it writes a bridge
// patch capturing the diff, updates the snapshot, and returns true.
// If the data is unchanged it returns false.
func checkBridge(histDir string, currentData []byte) (bool, error) {
	snap, err := readSnapshot(histDir)
	if err != nil {
		return false, err
	}

	// First run — no snapshot yet.
	if snap == nil {
		if wErr := writeSnapshot(histDir, currentData); wErr != nil {
			return false, wErr
		}
		return false, nil
	}

	// Compare as JSON maps to avoid false diffs from key ordering.
	same, err := jsonBytesEqual(snap, currentData)
	if err != nil {
		// If either side isn't valid JSON, fall back to byte comparison.
		same = bytes.Equal(snap, currentData)
	}
	if same {
		return false, nil
	}

	// External change detected — compute diff and write bridge patch.
	before, err := jsonBytesToMap(snap)
	if err != nil {
		before = map[string]any{}
	}
	after, err := jsonBytesToMap(currentData)
	if err != nil {
		after = map[string]any{}
	}

	fwd, rev := computePatch(before, after)
	bridge := &PatchRecord{
		Date:    time.Now().UTC(),
		Setting: "external_change",
		Forward: fwd,
		Reverse: rev,
	}
	if _, err := writePatch(histDir, bridge); err != nil {
		return false, err
	}
	if err := writeSnapshot(histDir, currentData); err != nil {
		return false, err
	}
	return true, nil
}

// jsonBytesEqual reports whether two JSON byte slices represent the same value
// by unmarshalling both and doing a deep comparison.
func jsonBytesEqual(a, b []byte) (bool, error) {
	ma, err := jsonBytesToMap(a)
	if err != nil {
		return false, err
	}
	mb, err := jsonBytesToMap(b)
	if err != nil {
		return false, err
	}
	return jsonEqual(ma, mb), nil
}

// jsonBytesToMap unmarshals JSON bytes into map[string]any.
func jsonBytesToMap(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}
	return m, nil
}
