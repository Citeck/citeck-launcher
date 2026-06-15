package cli

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newReloadCmd() *cobra.Command {
	var detach bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Hot-reload namespace configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureI18n()

			if dryRun {
				return runReloadDryRun()
			}

			c := client.TryNew(clientOpts())
			if c == nil || !c.IsRunning() {
				output.PrintText(t("cli.platformNotRunning"))
				return nil
			}
			defer c.Close()

			result, err := c.ReloadNamespace()
			if err != nil {
				return fmt.Errorf("reload: %w", err)
			}

			if !result.Success {
				return exitWithCode(ExitError, "reload failed: %s", result.Message)
			}

			output.PrintText(result.Message)

			if detach {
				return nil
			}

			// Wait for all services to stabilize.
			if waitErr := StreamReloadStatus(c); waitErr != nil {
				if errors.Is(waitErr, errInterrupted) {
					return nil // Changes apply in background.
				}
				return waitErr
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Don't wait for services to stabilize")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate config and show changes without applying")
	return cmd
}

// newDiffCmd is a convenience alias: `citeck diff` ≡ `citeck reload --dry-run`.
func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Show what a reload would change (alias for reload --dry-run)",
		RunE: func(_ *cobra.Command, _ []string) error {
			ensureI18n()
			return runReloadDryRun()
		},
	}
}

// runReloadDryRun validates namespace.yml and shows what a reload would do:
// the daemon's per-app reload plan (hash-based create/recreate/keep/remove/
// detached verdicts — the same diff a real reload performs). Degrades
// gracefully when the daemon is not running (config validation only) or does
// not expose the plan endpoint yet (legacy applied-config YAML diff).
func runReloadDryRun() error {
	// Validate config file
	nsPath := config.NamespaceConfigPath()
	nsCfg, err := namespace.LoadNamespaceConfig(nsPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if vErr := namespace.ValidateNamespaceConfig(nsCfg); vErr != nil {
		return fmt.Errorf("validation failed: %w", vErr)
	}
	if !output.IsJSON() {
		output.PrintText(t("reload.plan.config_valid", "path", nsPath))
	}

	c := client.TryNew(clientOpts())
	if c == nil || !c.IsRunning() {
		output.PrintText(t("reload.plan.no_daemon"))
		return nil
	}
	defer c.Close()

	plan, planErr := c.ReloadPlan()
	if planErr != nil {
		// Older daemon binary without the endpoint, or a transient failure —
		// fall back to the legacy applied-config YAML diff.
		output.PrintText(t("reload.plan.fetch_failed", "err", planErr.Error()))
		legacyAppliedConfigDiff(c, nsCfg)
		return nil
	}
	output.PrintResult(plan, func() { renderReloadPlan(plan) })
	return nil
}

// reasonDiffMaxItems caps the per-app REASON cell at this many change items
// before collapsing the tail into "+N more".
const reasonDiffMaxItems = 3

// renderReloadPlan prints the plan as APP | ACTION | REASON plus bundle and
// summary context lines.
func renderReloadPlan(plan *api.ReloadPlanDto) {
	if plan.BundleBefore != "" && plan.BundleAfter != "" && plan.BundleBefore != plan.BundleAfter {
		output.PrintText(t("reload.plan.bundle_change", "from", plan.BundleBefore, "to", plan.BundleAfter))
	}
	if plan.BundleFallback {
		output.PrintText(t("reload.plan.fallback_note"))
	}
	if plan.Summary.Create+plan.Summary.Recreate+plan.Summary.Remove == 0 {
		output.PrintText(t("reload.plan.no_changes"))
		return
	}

	headers := []string{
		t("reload.plan.header.app"),
		t("reload.plan.header.action"),
		t("reload.plan.header.reason"),
	}
	rows := make([][]string, 0, len(plan.Apps))
	snapshotHint := false
	for _, app := range plan.Apps {
		if app.SnapshotTag {
			snapshotHint = true
		}
		rows = append(rows, []string{app.Name, t("reload.plan.action." + app.Verdict), planRowReason(app)})
	}
	output.PrintText("%s", output.FormatTable(headers, rows))
	output.PrintText("")
	output.PrintText(t("reload.plan.summary",
		"create", strconv.Itoa(plan.Summary.Create),
		"recreate", strconv.Itoa(plan.Summary.Recreate),
		"keep", strconv.Itoa(plan.Summary.Keep),
		"remove", strconv.Itoa(plan.Summary.Remove),
		"detached", strconv.Itoa(plan.Summary.Detached)))
	if plan.WouldSkip {
		output.PrintText(t("reload.plan.would_skip"))
	}
	if snapshotHint {
		output.PrintText(t("reload.plan.snapshot_note"))
	}
}

// planRowReason builds the REASON cell for one plan row.
func planRowReason(app api.ReloadPlanAppDto) string {
	switch app.Verdict {
	case namespace.PlanVerdictCreate:
		return t("reload.plan.reason.new")
	case namespace.PlanVerdictRemove:
		return t("reload.plan.reason.removed")
	case namespace.PlanVerdictDetached:
		return t("reload.plan.reason.detached")
	case namespace.PlanVerdictRecreate:
		return summarizeHashDiff(app.DiffAdded, app.DiffRemoved, reasonDiffMaxItems)
	}
	return ""
}

// summarizeHashDiff compacts raw hash-input diff lines into a short
// key-level summary: "~ key" (value changed), "+ key" (added), "- key"
// (removed), capped at maxItems with a localized "+N more" tail. Keys are
// the part before the first '=' ("env:ECOS_X", "imageDigest", "vol"), which
// keeps long values (digests, JVM opts) out of the table while staying
// faithful to the underlying hash-input lines (the full lines are available
// via --format json).
func summarizeHashDiff(added, removed []string, maxItems int) string {
	addedKeys := hashLineKeys(added)
	removedKeys := hashLineKeys(removed)

	items := make([]string, 0, len(addedKeys)+len(removedKeys))
	seenChanged := make(map[string]bool, len(addedKeys))
	for _, k := range addedKeys {
		if removedKeys.contains(k) {
			if !seenChanged[k] {
				seenChanged[k] = true
				items = append(items, "~ "+k)
			}
			continue
		}
		items = append(items, "+ "+k)
	}
	for _, k := range removedKeys {
		if !addedKeys.contains(k) {
			items = append(items, "- "+k)
		}
	}

	if len(items) > maxItems {
		rest := len(items) - maxItems
		items = append(items[:maxItems], t("reload.plan.more", "count", strconv.Itoa(rest)))
	}
	return strings.Join(items, ", ")
}

// hashLineKeySet is an ordered key list with O(n) membership (n is tiny).
type hashLineKeySet []string

func (s hashLineKeySet) contains(k string) bool {
	return slices.Contains(s, k)
}

// hashLineKeys extracts the deduplicated key prefix of each hash-input line
// (text before the first '='; the whole line when it has none), preserving
// first-seen order.
func hashLineKeys(lines []string) hashLineKeySet {
	keys := make(hashLineKeySet, 0, len(lines))
	for _, l := range lines {
		key, _, _ := strings.Cut(l, "=")
		if !keys.contains(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// legacyAppliedConfigDiff is the pre-plan dry-run behavior: a YAML diff of the
// namespace.yml file against the daemon's applied config snapshot. Kept as the
// fallback when the daemon does not expose /namespace/reload-plan. Best-effort
// output only — every failure degrades to printing nothing further.
func legacyAppliedConfigDiff(c *client.DaemonClient, nsCfg *namespace.Config) {
	appliedYAML, err := c.GetAppliedConfig()
	if err != nil {
		output.PrintText("(cannot fetch applied config: %v)", err)
		return
	}

	// Normalize both sides through parse+marshal so the diff compares
	// identically-structured maps (defaults filled in, zero-value fields present).
	appliedCfg, parseErr := namespace.ParseNamespaceConfig([]byte(appliedYAML))
	if parseErr != nil {
		return
	}
	currentData, _ := namespace.MarshalNamespaceConfig(appliedCfg)
	nsData, _ := namespace.MarshalNamespaceConfig(nsCfg)

	var current, updated map[string]any
	if yaml.Unmarshal(currentData, &current) != nil {
		return
	}
	if yaml.Unmarshal(nsData, &updated) != nil {
		return
	}

	changes := diffMaps("", current, updated)
	if len(changes) == 0 {
		output.PrintText("No changes detected.")
		return
	}

	output.PrintText("Changes:")
	for _, ch := range changes {
		output.PrintText("  %s", ch)
	}
}

// diffMaps recursively compares two YAML maps and returns human-readable change descriptions.
func diffMaps(prefix string, old, updated map[string]any) []string {
	var changes []string

	for key, newVal := range updated {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		oldVal, exists := old[key]
		if !exists {
			changes = append(changes, fmt.Sprintf("+ %s: %v", path, newVal))
			continue
		}

		oldMap, oldIsMap := oldVal.(map[string]any)
		newMap, newIsMap := newVal.(map[string]any)
		if oldIsMap && newIsMap {
			changes = append(changes, diffMaps(path, oldMap, newMap)...)
		} else if !reflect.DeepEqual(oldVal, newVal) {
			changes = append(changes, fmt.Sprintf("~ %s: %v → %v", path, oldVal, newVal))
		}
	}

	for key := range old {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if _, exists := updated[key]; !exists {
			changes = append(changes, fmt.Sprintf("- %s", path))
		}
	}

	return changes
}
