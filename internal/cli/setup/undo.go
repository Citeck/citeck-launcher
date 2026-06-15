package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
)

// undoResultJSON is the `--format json` result of `citeck setup history --undo`.
type undoResultJSON struct {
	Status     string `json:"status"`
	UndoneID   string `json:"undoneId"`
	Setting    string `json:"setting"`
	File       string `json:"file"`
	NewEntryID string `json:"newEntryId"`
	OpsApplied int    `json:"opsApplied"`
	ReloadHint bool   `json:"reloadHint"`
}

// runHistoryUndo implements `citeck setup history --undo <id>`.
// In JSON mode failures are reported as a structured object (the root error
// handler suppresses plain-text errors when --format json is active).
func runHistoryUndo(id string, yes bool) error {
	err := doHistoryUndo(id, yes)
	if err != nil && output.IsJSON() {
		output.PrintJSON(map[string]any{"status": "error", "error": err.Error()})
	}
	return err
}

// doHistoryUndo applies the Reverse ops of the selected history entry to the
// current config, validates and writes the result atomically, and records the
// undo itself as a new history entry so it is undoable too.
func doHistoryUndo(id string, yes bool) error {
	patches, err := collectAllPatches()
	if err != nil {
		return err
	}

	entry, err := findHistoryEntry(patches, id)
	if err != nil {
		return err
	}
	rec := entry.Record

	if len(rec.Reverse) == 0 {
		return errors.New(i18n.T("setup.history.undo.nothing", "id", entry.FileName))
	}

	// Confirm before touching anything (--yes skips; JSON mode requires --yes
	// because an interactive prompt would corrupt the machine-readable output).
	if !yes {
		ok, cErr := confirmUndo(entry.FileName, rec)
		if cErr != nil {
			return cErr
		}
		if !ok {
			return nil
		}
	}

	cfgPath := configFilePath(entry.Target)
	newID, err := performUndo(entry.Target, cfgPath, historyDir(cfgPath), entry.FileName, rec)
	if err != nil {
		return err
	}

	reloadHint := entry.Target == NamespaceFile && daemonIsRunning()

	if output.IsJSON() {
		output.PrintJSON(undoResultJSON{
			Status:     "ok",
			UndoneID:   entry.FileName,
			Setting:    rec.Setting,
			File:       targetFileName(entry.Target),
			NewEntryID: newID,
			OpsApplied: len(rec.Reverse),
			ReloadHint: reloadHint,
		})
		return nil
	}

	if rec.SecretOps != nil {
		fmt.Println(i18n.T("setup.history.undo.secrets_warn")) //nolint:forbidigo // CLI output
	}
	fmt.Println(i18n.T("setup.history.undo.done", "id", newID)) //nolint:forbidigo // CLI output
	if reloadHint {
		fmt.Println(i18n.T("setup.history.undo.reload_hint")) //nolint:forbidigo // CLI output
	}
	return nil
}

// confirmUndo shows the ops that will be applied and asks for confirmation.
// Returns (false, nil) on a user abort (Esc/Ctrl+C) or a "No" answer.
func confirmUndo(entryID string, rec *PatchRecord) (bool, error) {
	if output.IsJSON() {
		return false, errors.New(i18n.T("setup.history.undo.yes_required"))
	}
	showDiff(rec.Reverse, rec.Forward)
	ok, err := (&prompt.Confirm{
		Title: i18n.T("setup.history.undo.confirm", "id", entryID),
		Hints: hints(),
	}).Run()
	if err != nil {
		if isUserAborted(err) {
			return false, nil
		}
		return false, fmt.Errorf("confirm undo: %w", err)
	}
	return ok, nil
}

// findHistoryEntry resolves a user-supplied entry ID (file name with or
// without the ".json" suffix) against the collected patches.
func findHistoryEntry(patches []indexedPatch, id string) (*indexedPatch, error) {
	want := strings.TrimSuffix(id, ".json")
	var match *indexedPatch
	for i := range patches {
		if patches[i].FileName != want {
			continue
		}
		if match != nil {
			return nil, errors.New(i18n.T("setup.history.undo.ambiguous", "id", want))
		}
		match = &patches[i]
	}
	if match == nil {
		return nil, errors.New(i18n.T("setup.history.undo.not_found", "id", want))
	}
	return match, nil
}

// performUndo applies rec.Reverse to the config at cfgPath, validates the
// result, writes it atomically, and records the undo as a new history entry
// (Forward = rec.Reverse, Reverse = rec.Forward) with an updated snapshot.
// Returns the new history entry ID.
//
// It refuses (without writing anything) when the reverse ops no longer apply
// cleanly — paths missing or values changed since the entry was recorded.
func performUndo(target TargetFile, cfgPath, histDir, undoneID string, rec *PatchRecord) (string, error) {
	curMap, err := loadConfigAsMap(target, cfgPath)
	if err != nil {
		return "", err
	}

	if cErr := checkReverseApplies(curMap, rec); cErr != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("setup.history.undo.stale", "id", undoneID), cErr)
	}
	if aErr := applyPatch(curMap, rec.Reverse); aErr != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("setup.history.undo.stale", "id", undoneID), aErr)
	}

	snapObj, wErr := validateAndWriteConfig(target, cfgPath, curMap)
	if wErr != nil {
		return "", wErr
	}

	// Record the undo itself as a new history entry so it is undoable too.
	undoRec := &PatchRecord{
		Date:    time.Now().UTC(),
		Setting: "undo_" + rec.Setting,
		Command: "setup history --undo " + undoneID,
		Input:   map[string]any{"undoneEntry": undoneID},
		Forward: rec.Reverse,
		Reverse: rec.Forward,
	}
	path, pErr := writePatch(histDir, undoRec)
	if pErr != nil {
		slog.Warn("Failed to write undo patch record", "err", pErr)
	}

	// Update the snapshot so the next bridge check doesn't flag the undo
	// as an external change.
	if snapJSON, jErr := json.MarshalIndent(structToJSONMapOrEmpty(snapObj), "", "  "); jErr == nil {
		if sErr := writeSnapshot(histDir, snapJSON); sErr != nil {
			slog.Warn("Failed to update snapshot after undo", "err", sErr)
		}
	}

	newID := patchFileName(undoRec.Date, undoRec.Setting)
	if path != "" {
		newID = filepath.Base(path)
	}
	return strings.TrimSuffix(newID, ".json"), nil
}

// loadConfigAsMap loads the target config and converts it to a JSON map —
// the representation patch ops are expressed in.
func loadConfigAsMap(target TargetFile, cfgPath string) (map[string]any, error) {
	if target == DaemonFile {
		cfg, err := config.LoadDaemonConfig()
		if err != nil {
			return nil, fmt.Errorf("load daemon config: %w", err)
		}
		return structToJSONMap(&cfg)
	}
	cfg, err := namespace.LoadNamespaceConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load namespace config: %w", err)
	}
	return structToJSONMap(cfg)
}

// validateAndWriteConfig converts the patched JSON map back into the typed
// config, validates it (namespace only — mirroring the setup flow), and
// writes it atomically. Returns the typed config for snapshot serialization.
func validateAndWriteConfig(target TargetFile, cfgPath string, curMap map[string]any) (any, error) {
	if target == DaemonFile {
		var cfg config.DaemonConfig
		if err := mapToStruct(curMap, &cfg); err != nil {
			return nil, fmt.Errorf("convert daemon config: %w", err)
		}
		if err := config.SaveDaemonConfig(cfg); err != nil {
			return nil, fmt.Errorf("save daemon config: %w", err)
		}
		return &cfg, nil
	}

	var cfg namespace.Config
	if err := mapToStruct(curMap, &cfg); err != nil {
		return nil, fmt.Errorf("convert namespace config: %w", err)
	}
	if vErr := namespace.ValidateNamespaceConfig(&cfg); vErr != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("setup.validation_error"), vErr)
	}
	data, mErr := namespace.MarshalNamespaceConfig(&cfg)
	if mErr != nil {
		return nil, fmt.Errorf("marshal namespace config: %w", mErr)
	}
	// 0o600: namespace.yml can carry plaintext secrets — keep it owner-only,
	// matching the setup flow's write.
	if wErr := fsutil.AtomicWriteFile(cfgPath, data, 0o600); wErr != nil {
		return nil, fmt.Errorf("write namespace config: %w", wErr)
	}
	return &cfg, nil
}

// checkReverseApplies verifies that rec.Reverse still applies cleanly to cur:
// every path it touches must still hold the value rec.Forward put there.
// The returned error describes the first conflict (technical detail; the
// caller wraps it with a localized message).
func checkReverseApplies(cur map[string]any, rec *PatchRecord) error {
	fwdByPath := make(map[string]PatchOp, len(rec.Forward))
	for _, op := range rec.Forward {
		fwdByPath[op.Path] = op
	}

	for _, op := range rec.Reverse {
		segments, err := splitJSONPointer(op.Path)
		if err != nil {
			return fmt.Errorf("invalid JSON Pointer %q: %w", op.Path, err)
		}
		if len(segments) == 0 {
			return fmt.Errorf("patch op %q on root document not supported", op.Op)
		}
		key := segments[len(segments)-1]
		parent, navErr := navigateTo(cur, segments[:len(segments)-1], false)

		switch op.Op {
		case "replace", "remove":
			if navErr != nil {
				return fmt.Errorf("path %s no longer exists: %w", op.Path, navErr)
			}
			val, exists := parent[key]
			if !exists {
				return fmt.Errorf("path %s no longer exists", op.Path)
			}
			// The forward op put a value there — it must still be intact.
			if fwd, ok := fwdByPath[op.Path]; ok && (fwd.Op == "replace" || fwd.Op == "add") && !jsonEqual(val, fwd.Value) {
				return fmt.Errorf("value at %s changed since this entry (now %v, expected %v)", op.Path, val, fwd.Value)
			}
		case "add":
			// Restores a key the forward patch removed — refuse if it has
			// reappeared with a different value in the meantime.
			if navErr == nil {
				if val, exists := parent[key]; exists && !jsonEqual(val, op.Value) {
					return fmt.Errorf("path %s was re-added with a different value (%v)", op.Path, val)
				}
			}
		default:
			return fmt.Errorf("unsupported patch op %q", op.Op)
		}
	}
	return nil
}

// mapToStruct converts a JSON map into a typed struct via JSON round-trip.
func mapToStruct(m map[string]any, out any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal config map: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unmarshal config map: %w", err)
	}
	return nil
}

// daemonIsRunning reports whether the daemon answers on its socket.
func daemonIsRunning() bool {
	c := client.TryNew(client.Options{})
	if c == nil {
		return false
	}
	defer c.Close()
	return c.IsRunning()
}
