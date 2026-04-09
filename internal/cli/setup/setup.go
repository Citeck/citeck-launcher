package setup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// NewSetupCmd creates the `citeck setup` command with history and rollback subcommands.
func NewSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup [setting]",
		Short: "Interactive configuration editor",
		Long:  "Opens an interactive menu to edit namespace or daemon settings. Pass a setting name to edit it directly.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runSetup,
	}

	cmd.AddCommand(newHistoryCmd())
	cmd.AddCommand(newRollbackCmd())

	return cmd
}

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "Show configuration change history",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			i18n.EnsureI18n()
			return runHistory()
		},
	}
}

func newRollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback [id]",
		Short: "Roll back a configuration change",
		Long:  "Rolls back the last change, or a specific change by patch filename. The rollback is recorded as a new patch.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			i18n.EnsureI18n()
			var id string
			if len(args) > 0 {
				id = args[0]
			}
			return runRollback(id)
		},
	}
}

// runSetup is the main entry point for `citeck setup`.
func runSetup(_ *cobra.Command, args []string) error {
	i18n.EnsureI18n()

	nsPath := config.NamespaceConfigPath()
	if _, err := os.Stat(nsPath); os.IsNotExist(err) {
		return fmt.Errorf("namespace.yml not found — run citeck install first")
	}

	nsCfg, err := namespace.LoadNamespaceConfig(nsPath)
	if err != nil {
		return fmt.Errorf("load namespace config: %w", err)
	}

	daemonCfg, err := config.LoadDaemonConfig()
	if err != nil {
		return fmt.Errorf("load daemon config: %w", err)
	}

	// Create setup context with resolved app list.
	sctx := &setupContext{
		PendingSecrets: make(map[string]string),
		CurrentApps:    resolveAppList(nsCfg),
	}

	if len(args) > 0 {
		return runSingleSetting(args[0], sctx, nsCfg, &daemonCfg)
	}
	return runMenuLoop(sctx, nsCfg, &daemonCfg)
}

// resolveAppList resolves the list of app names from the bundle, falling back to namespace config keys.
func resolveAppList(nsCfg *namespace.Config) []string {
	resolver := bundle.NewResolver(config.DataDir())
	resolver.SetOffline(true)

	result, err := resolver.Resolve(nsCfg.BundleRef)
	if err == nil && result.Bundle != nil && len(result.Bundle.Applications) > 0 {
		apps := make([]string, 0, len(result.Bundle.Applications))
		for name := range result.Bundle.Applications {
			apps = append(apps, name)
		}
		sort.Strings(apps)
		return apps
	}

	if err != nil {
		slog.Debug("Bundle resolve failed, falling back to namespace config", "err", err)
	}

	// Fallback: use keys from nsCfg.Webapps.
	apps := make([]string, 0, len(nsCfg.Webapps))
	for name := range nsCfg.Webapps {
		apps = append(apps, name)
	}
	sort.Strings(apps)
	return apps
}

// runMenuLoop shows an interactive menu of available settings in a loop.
func runMenuLoop(sctx *setupContext, nsCfg *namespace.Config, daemonCfg *config.DaemonConfig) error {
	for {
		settings := allSettings()

		// Build menu options: "id    — current value".
		var options []huh.Option[string]
		maxIDLen := 0
		for _, s := range settings {
			if !s.Available(nsCfg, sctx.CurrentApps) {
				continue
			}
			if len(s.ID()) > maxIDLen {
				maxIDLen = len(s.ID())
			}
		}

		for _, s := range settings {
			if !s.Available(nsCfg, sctx.CurrentApps) {
				continue
			}
			label := fmt.Sprintf("%-*s — %s", maxIDLen, s.ID(), s.CurrentValue(nsCfg, daemonCfg))
			options = append(options, huh.NewOption(label, s.ID()))
		}
		options = append(options, huh.NewOption(i18n.T("setup.exit"), ""))

		var choice string
		err := huh.NewSelect[string]().
			Title(i18n.T("setup.menu.title")).
			Options(options...).
			Value(&choice).
			Run()
		if err != nil {
			if isUserAborted(err) {
				return nil
			}
			return fmt.Errorf("menu selection: %w", err)
		}

		if choice == "" {
			return nil
		}

		if err := runSingleSetting(choice, sctx, nsCfg, daemonCfg); err != nil {
			return err
		}
	}
}

// runSingleSetting runs a single setting by ID, with diff, validation, confirmation, and persistence.
func runSingleSetting(id string, sctx *setupContext, nsCfg *namespace.Config, daemonCfg *config.DaemonConfig) error {
	// Find setting by ID.
	setting := findSettingByID(id)
	if setting == nil {
		return fmt.Errorf("unknown setting: %q", id)
	}

	// Deep copy "before" state.
	nsBefore, err := deepCopyConfig(nsCfg)
	if err != nil {
		return fmt.Errorf("copy namespace config: %w", err)
	}
	daemonBefore, err := deepCopyDaemonConfig(daemonCfg)
	if err != nil {
		return fmt.Errorf("copy daemon config: %w", err)
	}

	restore := func() {
		*nsCfg = *nsBefore
		*daemonCfg = *daemonBefore
	}

	// Clear pending secrets before each setting run.
	clear(sctx.PendingSecrets)

	// Run the setting's interactive prompt.
	if runErr := setting.Run(sctx, nsCfg, daemonCfg); runErr != nil {
		if isUserAborted(runErr) {
			restore()
			return nil
		}
		return fmt.Errorf("run setting %s: %w", id, runErr)
	}

	// Compute the config diff.
	target := setting.TargetFile()
	forward, reverse, err := computeSettingDiff(target, nsBefore, nsCfg, daemonBefore, daemonCfg)
	if err != nil {
		return err
	}

	// If no changes and no pending secrets, nothing to do.
	if len(forward) == 0 && len(sctx.PendingSecrets) == 0 {
		fmt.Println(i18n.T("setup.no_changes"))
		return nil
	}

	// Show diff.
	showDiff(forward, reverse)

	// Validate the modified config.
	if target == NamespaceFile {
		if vErr := namespace.ValidateNamespaceConfig(nsCfg); vErr != nil {
			fmt.Printf("%s: %v\n", i18n.T("setup.validation_error"), vErr)
			restore()
			return nil
		}
	}

	// Confirm.
	confirmed, err := showAndConfirmChanges()
	if err != nil {
		if isUserAborted(err) {
			restore()
			return nil
		}
		return err
	}
	if !confirmed {
		restore()
		return nil
	}

	// Write changes to disk.
	cfgPath := configFilePath(target)
	histDir := historyDir(cfgPath)

	secretOps, wErr := writeSettingChanges(sctx, target, cfgPath, histDir, nsCfg, daemonCfg, nsBefore, daemonBefore)
	if wErr != nil {
		return wErr
	}

	// Record patch and update snapshot.
	recordSettingPatch(id, histDir, target, forward, reverse, nsCfg, daemonCfg, secretOps)

	fmt.Println(i18n.T("setup.applied"))
	return nil
}

// findSettingByID returns the Setting with the given ID, or nil if not found.
func findSettingByID(id string) Setting {
	for _, s := range allSettings() {
		if s.ID() == id {
			return s
		}
	}
	return nil
}

// computeSettingDiff computes forward/reverse patches between before and after config states.
func computeSettingDiff(target TargetFile,
	nsBefore, nsCfg *namespace.Config,
	daemonBefore, daemonCfg *config.DaemonConfig,
) (forward, reverse []PatchOp, err error) {
	var beforeObj, afterObj any
	if target == DaemonFile {
		beforeObj, afterObj = daemonBefore, daemonCfg
	} else {
		beforeObj, afterObj = nsBefore, nsCfg
	}

	beforeMap, err := structToJSONMap(beforeObj)
	if err != nil {
		return nil, nil, fmt.Errorf("serialize before config: %w", err)
	}
	afterMap, err := structToJSONMap(afterObj)
	if err != nil {
		return nil, nil, fmt.Errorf("serialize after config: %w", err)
	}

	forward, reverse = computePatch(beforeMap, afterMap)
	return forward, reverse, nil
}

// showDiff prints the forward patch operations to stdout.
func showDiff(forward, reverse []PatchOp) {
	if len(forward) == 0 {
		return
	}
	fmt.Println()
	for _, op := range forward {
		switch op.Op {
		case "add":
			fmt.Printf("  + %s = %v\n", op.Path, op.Value)
		case "remove":
			fmt.Printf("  - %s\n", op.Path)
		case "replace":
			var oldVal any
			for _, rev := range reverse {
				if rev.Path == op.Path {
					oldVal = rev.Value
					break
				}
			}
			fmt.Printf("  ~ %s = %v (was %v)\n", op.Path, op.Value, oldVal)
		}
	}
	fmt.Println()
}

// showAndConfirmChanges prompts the user to confirm applying the changes.
func showAndConfirmChanges() (bool, error) {
	var confirm bool
	err := huh.NewConfirm().
		Title(i18n.T("setup.confirm")).
		Value(&confirm).
		Run()
	if err != nil {
		return false, fmt.Errorf("confirm: %w", err)
	}
	return confirm, nil
}

// writeSettingChanges writes the modified config to disk and pending secrets.
// beforeNsCfg/beforeDaemonCfg are the "before" snapshots used for bridge detection.
func writeSettingChanges(sctx *setupContext, target TargetFile, cfgPath, histDir string,
	nsCfg *namespace.Config, daemonCfg *config.DaemonConfig,
	beforeNsCfg *namespace.Config, beforeDaemonCfg *config.DaemonConfig,
) (*SecretOps, error) {
	// Bridge check — detect external changes since last snapshot.
	// Convert the "before" config to JSON so it matches the snapshot format.
	var beforeObj any
	if target == DaemonFile {
		beforeObj = beforeDaemonCfg
	} else {
		beforeObj = beforeNsCfg
	}
	beforeJSON, jErr := json.MarshalIndent(structToJSONMapOrEmpty(beforeObj), "", "  ")
	if jErr == nil {
		bridged, bErr := checkBridge(histDir, beforeJSON)
		if bErr != nil {
			slog.Warn("Bridge check failed", "err", bErr)
		} else if bridged {
			slog.Info("Recorded external config change as bridge patch")
		}
	}

	// Write config.
	if target == DaemonFile {
		if sErr := config.SaveDaemonConfig(*daemonCfg); sErr != nil {
			return nil, fmt.Errorf("save daemon config: %w", sErr)
		}
	} else {
		data, mErr := namespace.MarshalNamespaceConfig(nsCfg)
		if mErr != nil {
			return nil, fmt.Errorf("marshal namespace config: %w", mErr)
		}
		if wErr := fsutil.AtomicWriteFile(cfgPath, data, 0o644); wErr != nil {
			return nil, fmt.Errorf("write namespace config: %w", wErr)
		}
	}

	// Write secrets via daemon API (if running) or local SecretService.
	secretOps, sErr := writePendingSecrets(sctx)
	if sErr != nil {
		slog.Error("Failed to write secrets", "err", sErr)
		fmt.Printf("  %s: %v\n", i18n.T("setup.secret_write_error"), sErr)
	}

	return secretOps, nil
}

// recordSettingPatch records the patch in history and updates the snapshot.
func recordSettingPatch(id, histDir string, target TargetFile,
	forward, reverse []PatchOp,
	nsCfg *namespace.Config, daemonCfg *config.DaemonConfig,
	secretOps *SecretOps,
) {
	// Build input from the "after" config state based on which setting was used.
	input := buildSettingInput(id, nsCfg, daemonCfg)

	patch := &PatchRecord{
		Date:      time.Now().UTC(),
		Setting:   id,
		Command:   "setup " + id,
		Input:     input,
		Forward:   forward,
		Reverse:   reverse,
		SecretOps: secretOps,
	}

	if _, err := writePatch(histDir, patch); err != nil {
		slog.Warn("Failed to write patch record", "err", err)
	}

	// Update snapshot with current config state.
	var snapData []byte
	if target == DaemonFile {
		snapJSON, jErr := json.MarshalIndent(structToJSONMapOrEmpty(daemonCfg), "", "  ")
		if jErr == nil {
			snapData = snapJSON
		}
	} else {
		snapJSON, jErr := json.MarshalIndent(structToJSONMapOrEmpty(nsCfg), "", "  ")
		if jErr == nil {
			snapData = snapJSON
		}
	}
	if snapData != nil {
		if err := writeSnapshot(histDir, snapData); err != nil {
			slog.Warn("Failed to update snapshot", "err", err)
		}
	}
}

// buildSettingInput builds a map of user-provided input for a given setting ID.
// Secret values are masked for safe storage in patch records.
func buildSettingInput(id string, nsCfg *namespace.Config, dcfg *config.DaemonConfig) map[string]any {
	input := make(map[string]any)
	switch id {
	case "hostname":
		input["host"] = nsCfg.Proxy.Host
	case "port":
		input["port"] = nsCfg.Proxy.Port
	case "email":
		if nsCfg.Email != nil {
			input["host"] = nsCfg.Email.Host
			input["port"] = nsCfg.Email.Port
			input["from"] = nsCfg.Email.From
			input["password"] = "***"
		}
	case "s3":
		if nsCfg.S3 != nil {
			input["endpoint"] = nsCfg.S3.Endpoint
			input["bucket"] = nsCfg.S3.Bucket
			input["secretKey"] = "***"
		}
	case "tls":
		input["enabled"] = nsCfg.Proxy.TLS.Enabled
		input["letsEncrypt"] = nsCfg.Proxy.TLS.LetsEncrypt
	case "auth":
		input["type"] = string(nsCfg.Authentication.Type)
	case "language":
		input["locale"] = dcfg.Locale
	case "resources":
		for name, wp := range nsCfg.Webapps {
			if wp.HeapSize != "" || wp.MemoryLimit != "" {
				input[name] = map[string]any{"heapSize": wp.HeapSize, "memoryLimit": wp.MemoryLimit}
			}
		}
	}
	return input
}

// structToJSONMapOrEmpty converts a struct to a JSON map, returning an empty map on error.
func structToJSONMapOrEmpty(v any) map[string]any {
	m, err := structToJSONMap(v)
	if err != nil {
		return map[string]any{}
	}
	return m
}

// indexedPatch pairs a PatchRecord with its source file and target.
type indexedPatch struct {
	Record   *PatchRecord
	FileName string
	Target   TargetFile
}

// runHistory lists all configuration change patches sorted by date.
func runHistory() error {
	patches, err := collectAllPatches()
	if err != nil {
		return err
	}

	if len(patches) == 0 {
		fmt.Println(i18n.T("setup.history.empty"))
		return nil
	}

	headers := []string{"DATE", "SETTING", "FILE", "DESCRIPTION"}
	rows := make([][]string, 0, len(patches))

	for _, p := range patches {
		date := p.Record.Date.Format("2006-01-02 15:04:05")
		file := "namespace.yml"
		if p.Target == DaemonFile {
			file = "daemon.yml"
		}

		desc := patchDescription(p.Record.Forward)
		rows = append(rows, []string{date, p.Record.Setting, file, desc})
	}

	fmt.Println(output.FormatTable(headers, rows))
	return nil
}

// patchDescription generates a brief text description from patch operations.
func patchDescription(ops []PatchOp) string {
	if len(ops) == 0 {
		return "(no changes)"
	}
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		switch op.Op {
		case "add":
			parts = append(parts, fmt.Sprintf("add %s = %v", op.Path, op.Value))
		case "remove":
			parts = append(parts, fmt.Sprintf("remove %s", op.Path))
		case "replace":
			parts = append(parts, fmt.Sprintf("replace %s = %v", op.Path, op.Value))
		}
	}
	desc := strings.Join(parts, "; ")
	if len(desc) > 80 {
		desc = desc[:77] + "..."
	}
	return desc
}

// collectAllPatches collects patches from both namespace and daemon history dirs, sorted by date.
func collectAllPatches() ([]indexedPatch, error) {
	var all []indexedPatch

	targets := []TargetFile{NamespaceFile, DaemonFile}
	for _, t := range targets {
		cfgPath := configFilePath(t)
		hDir := historyDir(cfgPath)
		patches, err := listPatches(hDir)
		if err != nil {
			return nil, fmt.Errorf("list patches for %s: %w", cfgPath, err)
		}
		for _, p := range patches {
			all = append(all, indexedPatch{
				Record:   p,
				FileName: patchFileName(p.Date, p.Setting),
				Target:   t,
			})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Record.Date.Before(all[j].Record.Date)
	})
	return all, nil
}

// runRollback rolls back a specific patch (by filename/id) or the most recent one.
func runRollback(id string) error {
	patches, err := collectAllPatches()
	if err != nil {
		return err
	}

	if len(patches) == 0 {
		return fmt.Errorf("no patches to roll back")
	}

	// Find the target patch.
	var target *indexedPatch
	if id == "" {
		// Last patch.
		target = &patches[len(patches)-1]
	} else {
		// Search by filename prefix match.
		for i := range patches {
			if strings.Contains(patches[i].FileName, id) {
				target = &patches[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("patch not found: %q", id)
		}
	}

	if len(target.Record.Reverse) == 0 {
		return fmt.Errorf("patch has no reverse operations — cannot roll back")
	}

	// Show what will be rolled back.
	fmt.Printf("%s: %s (%s)\n", i18n.T("setup.rollback.target"), target.Record.Setting,
		target.Record.Date.Format("2006-01-02 15:04:05"))
	fmt.Println()
	for _, op := range target.Record.Reverse {
		switch op.Op {
		case "add":
			fmt.Printf("  + %s = %v\n", op.Path, op.Value)
		case "remove":
			fmt.Printf("  - %s\n", op.Path)
		case "replace":
			fmt.Printf("  ~ %s = %v\n", op.Path, op.Value)
		}
	}
	fmt.Println()

	// Confirm.
	var confirm bool
	if cErr := huh.NewConfirm().
		Title(i18n.T("setup.rollback.confirm")).
		Value(&confirm).
		Run(); cErr != nil {
		if isUserAborted(cErr) {
			return nil
		}
		return fmt.Errorf("rollback confirm: %w", cErr)
	}
	if !confirm {
		return nil
	}

	// Load current config as JSON map, apply reverse patch, write back.
	cfgPath := configFilePath(target.Target)

	if target.Target == DaemonFile {
		if err := rollbackDaemonConfig(cfgPath, target); err != nil {
			return err
		}
	} else {
		if err := rollbackNamespaceConfig(cfgPath, target); err != nil {
			return err
		}
	}

	fmt.Println(i18n.T("setup.rollback.applied"))
	return nil
}

// rollbackNamespaceConfig applies a reverse patch to the namespace config.
func rollbackNamespaceConfig(cfgPath string, target *indexedPatch) error {
	nsCfg, err := namespace.LoadNamespaceConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load namespace config: %w", err)
	}

	jsonMap, err := structToJSONMap(nsCfg)
	if err != nil {
		return fmt.Errorf("serialize namespace config: %w", err)
	}

	if conflict := detectRollbackConflict(jsonMap, target.Record.Forward); conflict != "" {
		forced, fErr := confirmForceRollback(conflict)
		if fErr != nil {
			return fErr
		}
		if !forced {
			return nil
		}
	}

	if pErr := applyPatch(jsonMap, target.Record.Reverse); pErr != nil {
		return fmt.Errorf("apply reverse patch: %w", pErr)
	}

	// Convert back: JSON map -> JSON bytes -> namespace.Config -> YAML.
	jsonBytes, err := json.Marshal(jsonMap)
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}
	var restored namespace.Config
	if uErr := json.Unmarshal(jsonBytes, &restored); uErr != nil {
		return fmt.Errorf("unmarshal patched config: %w", uErr)
	}

	if vErr := namespace.ValidateNamespaceConfig(&restored); vErr != nil {
		return fmt.Errorf("validation failed after rollback: %w", vErr)
	}

	data, err := namespace.MarshalNamespaceConfig(&restored)
	if err != nil {
		return fmt.Errorf("marshal namespace config: %w", err)
	}

	if wErr := fsutil.AtomicWriteFile(cfgPath, data, 0o644); wErr != nil {
		return fmt.Errorf("write namespace config: %w", wErr)
	}

	// Restore secrets from backup if the patch recorded secret operations.
	if target.Record.SecretOps != nil && len(target.Record.SecretOps.Reverse) > 0 {
		rollbackSecrets(target.Record.SecretOps.Reverse)
	}

	// Record rollback as new patch and update snapshot.
	return recordRollbackPatch(cfgPath, target, &restored)
}

// rollbackDaemonConfig applies a reverse patch to the daemon config.
func rollbackDaemonConfig(cfgPath string, target *indexedPatch) error {
	daemonCfg, err := config.LoadDaemonConfig()
	if err != nil {
		return fmt.Errorf("load daemon config: %w", err)
	}

	jsonMap, err := structToJSONMap(&daemonCfg)
	if err != nil {
		return fmt.Errorf("serialize daemon config: %w", err)
	}

	if conflict := detectRollbackConflict(jsonMap, target.Record.Forward); conflict != "" {
		forced, fErr := confirmForceRollback(conflict)
		if fErr != nil {
			return fErr
		}
		if !forced {
			return nil
		}
	}

	if pErr := applyPatch(jsonMap, target.Record.Reverse); pErr != nil {
		return fmt.Errorf("apply reverse patch: %w", pErr)
	}

	// Convert back: JSON map -> JSON bytes -> config.DaemonConfig -> save.
	jsonBytes, err := json.Marshal(jsonMap)
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}
	var restored config.DaemonConfig
	if uErr := json.Unmarshal(jsonBytes, &restored); uErr != nil {
		return fmt.Errorf("unmarshal patched config: %w", uErr)
	}

	if sErr := config.SaveDaemonConfig(restored); sErr != nil {
		return fmt.Errorf("save daemon config: %w", sErr)
	}

	// Restore secrets from backup if the patch recorded secret operations.
	if target.Record.SecretOps != nil && len(target.Record.SecretOps.Reverse) > 0 {
		rollbackSecrets(target.Record.SecretOps.Reverse)
	}

	// Record rollback as new patch and update snapshot.
	return recordRollbackPatch(cfgPath, target, &restored)
}

// recordRollbackPatch writes a new patch that records the rollback and updates the snapshot.
func recordRollbackPatch(cfgPath string, target *indexedPatch, cfg any) error {
	hDir := historyDir(cfgPath)

	// Rollback patch: forward = reverse of original, reverse = forward of original.
	rollbackRec := &PatchRecord{
		Date:    time.Now().UTC(),
		Setting: "rollback:" + target.Record.Setting,
		Command: fmt.Sprintf("setup rollback %s", filepath.Base(target.FileName)),
		Input:   map[string]any{"rollback_of": target.Record.Date.Format(time.RFC3339)},
		Forward: target.Record.Reverse,
		Reverse: target.Record.Forward,
	}

	if _, err := writePatch(hDir, rollbackRec); err != nil {
		slog.Warn("Failed to write rollback patch", "err", err)
	}

	snapJSON, err := json.MarshalIndent(structToJSONMapOrEmpty(cfg), "", "  ")
	if err == nil {
		if err := writeSnapshot(hDir, snapJSON); err != nil {
			slog.Warn("Failed to update snapshot after rollback", "err", err)
		}
	}

	return nil
}

// detectRollbackConflict checks if the current config values at forward patch paths
// match the expected forward values. Returns a description of the first conflict found,
// or empty string if no conflicts.
func detectRollbackConflict(currentMap map[string]any, forwardOps []PatchOp) string {
	for _, op := range forwardOps {
		if op.Op != "replace" && op.Op != "add" {
			continue
		}
		segments, err := splitJSONPointer(op.Path)
		if err != nil || len(segments) == 0 {
			continue
		}
		// Navigate to parent, then read the key.
		cur := currentMap
		depth := 0
		for _, seg := range segments[:len(segments)-1] {
			child, ok := cur[seg]
			if !ok {
				break
			}
			childMap, ok := child.(map[string]any)
			if !ok {
				break
			}
			cur = childMap
			depth++
		}
		// If we couldn't navigate to the correct depth, the parent was removed.
		if depth < len(segments)-1 {
			return fmt.Sprintf("%s: parent path missing", op.Path)
		}
		key := segments[len(segments)-1]
		actual, exists := cur[key]
		if op.Op == "add" && !exists {
			// Value was expected to have been added but is now gone — conflict.
			return fmt.Sprintf("%s: expected %v but key does not exist", op.Path, op.Value)
		}
		if exists && !jsonEqual(actual, op.Value) {
			return fmt.Sprintf("%s: expected %v but found %v", op.Path, op.Value, actual)
		}
	}
	return ""
}

// confirmForceRollback warns the user about a conflict and asks whether to force the rollback.
func confirmForceRollback(conflict string) (bool, error) {
	fmt.Printf("\n%s: %s\n\n", i18n.T("setup.rollback.conflict"), conflict)
	var force bool
	if err := huh.NewConfirm().
		Title(i18n.T("setup.rollback.force")).
		Value(&force).
		Run(); err != nil {
		if isUserAborted(err) {
			return false, nil
		}
		return false, fmt.Errorf("force rollback confirm: %w", err)
	}
	return force, nil
}
