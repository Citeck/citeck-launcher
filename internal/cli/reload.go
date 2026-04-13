package cli

import (
	"errors"
	"fmt"
	"reflect"

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

// runReloadDryRun validates namespace.yml and shows what would change on reload.
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
	output.PrintText("Config valid: %s", nsPath)

	// Show diff against running config
	c := client.TryNew(clientOpts())
	if c == nil || !c.IsRunning() {
		output.PrintText("(platform not running — cannot show diff)")
		return nil
	}
	defer c.Close()

	// Fetch the applied config — the snapshot that was used to generate
	// the currently running containers. This lets dry-run show real diffs
	// between what's running and what the file now says.
	appliedYAML, err := c.GetAppliedConfig()
	if err != nil {
		output.PrintText("(cannot fetch applied config: %v)", err)
		return nil
	}

	// Normalize both sides through parse+marshal so the diff compares
	// identically-structured maps (defaults filled in, zero-value fields present).
	appliedCfg, parseErr := namespace.ParseNamespaceConfig([]byte(appliedYAML))
	if parseErr != nil {
		return nil
	}
	currentData, _ := namespace.MarshalNamespaceConfig(appliedCfg)
	nsData, _ := namespace.MarshalNamespaceConfig(nsCfg)

	var current, updated map[string]any
	if yaml.Unmarshal(currentData, &current) != nil {
		return nil
	}
	if yaml.Unmarshal(nsData, &updated) != nil {
		return nil
	}

	changes := diffMaps("", current, updated)
	if len(changes) == 0 {
		output.PrintText("No changes detected.")
		return nil
	}

	output.PrintText("Changes:")
	for _, ch := range changes {
		output.PrintText("  %s", ch)
	}
	return nil
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
