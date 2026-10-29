package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
)

// `citeck features` is the server-mode surface of the namespace dialog's
// "Experimental" tab: the BETA modules the active namespace's bundle offers,
// which of them are on, and the switch. It is built on the daemon API the
// dialog uses — GET /experimental-features for the offers (asked with no
// bundle, so the daemon answers about the active namespace and fills
// Enabled) and PUT /namespaces/{id}/edit for the change.
//
// The PUT carries the resulting state of every OFFERED feature and nothing
// else: the daemon keeps the stored ids its bundle does not offer (the ones a
// later release graduated) on its own, and sending them would have it refuse
// the edit as naming features the bundle does not know.

// featuresClient is the daemon surface the command needs, narrowed so the
// flows can be tested against a fake without a socket.
type featuresClient interface {
	GetNamespace() (*api.NamespaceDto, error)
	GetExperimentalFeatures() (*api.ExperimentalFeaturesDto, error)
	PutNamespaceEdit(nsID string, edit api.NamespaceEditDto) (*api.ActionResultDto, error)
}

// featuresToggleReport is the JSON verdict of `features enable|disable`: one
// object a script can read without parsing prose.
type featuresToggleReport struct {
	ID string `json:"id"`
	// Enabled is the feature's state AFTER the command.
	Enabled bool `json:"enabled"`
	// Changed is false when the feature was already in the requested state
	// and nothing was sent to the daemon.
	Changed bool `json:"changed"`
	// NamespaceStatus is the namespace's status at the time of the change:
	// STOPPED means the modules wait for the next start.
	NamespaceStatus string   `json:"namespaceStatus"`
	Apps            []string `json:"apps"`
}

func newFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "List and switch the experimental (beta) modules of the namespace's release",
		Long: "Lists the experimental (beta) modules the active namespace's bundle offers and which of\n" +
			"them are switched on. `citeck features enable <id>` switches one on, `disable` switches it\n" +
			"off; the namespace is reloaded, and a stopped namespace picks the change up on its next\n" +
			"start. A beta module may change or be removed in a later release.",
		RunE: func(_ *cobra.Command, _ []string) error { return runFeaturesList() },
	}
	cmd.AddCommand(newFeaturesListCmd(), newFeaturesToggleCmd("enable", true), newFeaturesToggleCmd("disable", false))
	return cmd
}

func newFeaturesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the experimental (beta) modules the bundle offers and which are on",
		RunE:  func(_ *cobra.Command, _ []string) error { return runFeaturesList() },
	}
}

func newFeaturesToggleCmd(verb string, on bool) *cobra.Command {
	short := "Switch an experimental (beta) module on"
	if !on {
		short = "Switch an experimental (beta) module off"
	}
	return &cobra.Command{
		Use:   verb + " <feature>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ensureI18n()
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()
			return runFeaturesToggle(c, args[0], on)
		},
	}
}

func runFeaturesList() error {
	ensureI18n()
	c, err := client.New(clientOpts())
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer c.Close()

	dto, err := c.GetExperimentalFeatures()
	if err != nil {
		return fmt.Errorf("list experimental features: %w", err)
	}
	// The DTO verbatim in JSON mode — Reason included, so a script sees the
	// unresolved bundle for what it is rather than as an empty list.
	output.PrintResult(dto, func() {
		for _, line := range featuresListLines(dto) {
			output.PrintText("%s", line)
		}
	})
	return nil
}

// featuresListLines is the text rendering: one table row per feature, each
// module's own description under the table (a cell is padded to its widest
// value, and a description is a sentence), then the legend. An unresolved
// bundle is one sentence naming the way out.
func featuresListLines(dto *api.ExperimentalFeaturesDto) []string {
	if dto.Reason == api.ExperimentalFeaturesBundleUnresolved {
		return []string{output.Colorize(output.Yellow, t("features.bundleUnresolved"))}
	}
	if len(dto.Features) == 0 {
		return []string{t("features.empty")}
	}
	rows := make([][]string, 0, len(dto.Features))
	descriptions := make([]string, 0, len(dto.Features))
	for _, f := range dto.Features {
		state := output.Colorize(output.Dim, t("features.state.off"))
		if f.Enabled {
			state = output.Colorize(output.Green, t("features.state.on"))
		}
		rows = append(rows, []string{f.ID, f.Title, strings.Join(f.Apps, ", "), state})
		if f.Description != "" {
			descriptions = append(descriptions, output.Colorize(output.Dim,
				t("features.descriptionLine", "id", f.ID, "description", f.Description)))
		}
	}
	lines := make([]string, 0, len(descriptions)+2)
	lines = append(lines, output.FormatTable([]string{
		t("features.col.id"), t("features.col.title"), t("features.col.apps"), t("features.col.state"),
	}, rows))
	lines = append(lines, descriptions...)
	lines = append(lines, output.Colorize(output.Dim, t("features.legend")))
	return lines
}

// errFeatureRefused is the local refusal: the id is not one the bundle
// offers, or there is nothing to switch. The daemon would refuse the PUT too
// (EXPERIMENTAL_FEATURE_UNKNOWN); refusing here names what IS offered.
var errFeatureRefused = errors.New("experimental feature refused")

// runFeaturesToggle switches one feature on or off. It reads the active
// namespace (for its id and status), the offered features with their state,
// toggles the one id and sends the resulting state of every offered feature.
func runFeaturesToggle(c featuresClient, id string, on bool) error {
	ns, err := c.GetNamespace()
	if err != nil {
		return fmt.Errorf("read namespace: %w", err)
	}
	if ns.ID == "" {
		return fmt.Errorf("%w: %s", errFeatureRefused, t("features.noNamespace"))
	}
	dto, err := c.GetExperimentalFeatures()
	if err != nil {
		return fmt.Errorf("list experimental features: %w", err)
	}
	if dto.Reason == api.ExperimentalFeaturesBundleUnresolved {
		return fmt.Errorf("%w: %s", errFeatureRefused, t("features.bundleUnresolved"))
	}

	var target *api.ExperimentalFeatureDto
	enabled := make([]string, 0, len(dto.Features))
	offered := make([]string, 0, len(dto.Features))
	for i := range dto.Features {
		f := &dto.Features[i]
		offered = append(offered, f.ID)
		if f.ID == id {
			target = f
			if on {
				enabled = append(enabled, f.ID)
			}
			continue
		}
		if f.Enabled {
			enabled = append(enabled, f.ID)
		}
	}
	if target == nil {
		if len(offered) == 0 {
			return fmt.Errorf("%w: %s", errFeatureRefused, t("features.noneOffered", "id", id))
		}
		return fmt.Errorf("%w: %s", errFeatureRefused,
			t("features.unknown", "id", id, "list", strings.Join(offered, ", ")))
	}

	report := featuresToggleReport{ID: id, Enabled: on, NamespaceStatus: ns.Status, Apps: target.Apps}
	if target.Enabled == on {
		key := "features.alreadyOff"
		if on {
			key = "features.alreadyOn"
		}
		output.PrintResult(report, func() { output.PrintText("%s", t(key, "id", id)) })
		return nil
	}

	if _, err := c.PutNamespaceEdit(ns.ID, api.NamespaceEditDto{EnabledExperimentalFeatures: &enabled}); err != nil {
		return fmt.Errorf("update experimental features: %w", err)
	}
	report.Changed = true
	output.PrintResult(report, func() {
		output.PrintText("%s", t(featuresToggleMessageKey(on, ns.Status), "id", id, "apps", strings.Join(target.Apps, ", ")))
	})
	return nil
}

// featuresToggleMessageKey picks the sentence for a change that landed. The
// daemon reloads the active namespace either way; what differs is whether
// containers move now (a namespace that is up) or on the next start.
func featuresToggleMessageKey(on bool, nsStatus string) string {
	stopped := nsStatus == api.NsStatusStopped
	switch {
	case on && stopped:
		return "features.enabled.stopped"
	case on:
		return "features.enabled.running"
	case stopped:
		return "features.disabled.stopped"
	default:
		return "features.disabled.running"
	}
}
