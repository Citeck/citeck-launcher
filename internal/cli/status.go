package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// editedLegend explains the "*" marker that FormatAppTable appends to apps
// carrying a user config edit, and points at the reset command. Plain English
// (like the surrounding "Name:"/"Status:" labels) to avoid 8-locale churn.
const editedLegend = "* config edited — `citeck edit <app> --reset` reverts an override; " +
	"reset an edited file in the app's config editor"

// statusExtrasClient is the daemon surface `citeck status` needs on TOP of the
// namespace DTO. Narrowed to the two calls so the policy below can be tested
// without a daemon.
type statusExtrasClient interface {
	GetLicenseStatus() (*api.LicenseStatusDto, error)
	GetDependencies() (*api.DependenciesDto, error)
}

// fetchStatusExtras gets the license state and the dependency state that the
// TEXT rendering adds to the namespace — and gets neither in JSON mode, where
// output.PrintResult marshals the namespace DTO alone. The two requests would
// buy output that is never produced, against a daemon a scripted caller is
// typically polling in a loop. (The JSON shape is deliberately left as that
// one type: NamespaceDto already carries DependencyUpgrades and
// DependencyMigration, so the machine contract is not missing the dependency
// state it reports — widening it here would change what every existing
// `--format json` consumer parses.)
//
// Both are best-effort in text mode too: an older daemon does not expose the
// endpoint, a locked secret store errors, and a namespace that is not
// configured yet answers 400 for the dependencies. Each failure drops its own
// line rather than failing the command. The dependency list is fetched rather
// than read off the namespace DTO because a PENDING ROLLBACK lives only in the
// dependencies DTO, and it is the one dependency state the operator must act
// on.
func fetchStatusExtras(c statusExtrasClient) (*api.LicenseStatusDto, *api.DependenciesDto) {
	if output.IsJSON() {
		return nil, nil
	}
	licStatus, licErr := c.GetLicenseStatus()
	if licErr != nil {
		licStatus = nil
	}
	depsDto, depsErr := c.GetDependencies()
	if depsErr != nil {
		depsDto = nil
	}
	return licStatus, depsDto
}

func newStatusCmd() *cobra.Command {
	var watch bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show namespace and app status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureI18n()
			c := client.TryNew(clientOpts())
			if c == nil {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText(t("cli.platformNotRunning"))
				}
				return nil
			}
			defer c.Close()

			if !c.IsRunning() {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText(t("cli.platformNotRunning"))
				}
				return nil
			}

			// Watch mode takes over rendering so it can clear/redraw
			// on each event. Skipping the pre-watch PrintResult here
			// prevents the initial output from leaking above the live
			// table (untracked lines → never cleared).
			if watch {
				return watchEvents(c)
			}

			ns, err := c.GetNamespace()
			if err != nil {
				return fmt.Errorf("get namespace: %w", err)
			}

			licStatus, depsDto := fetchStatusExtras(c)

			output.PrintResult(ns, func() {
				for _, line := range statusHeaderLines(ns, licStatus, depsDto) {
					output.PrintText("%s", line)
				}

				if len(ns.Apps) > 0 {
					fmt.Println()
					r := output.FormatAppTable(ns.Apps)
					output.PrintText(r.Table)
					if r.AnyEdited {
						output.PrintText(output.Colorize(output.Dim, editedLegend))
					}
				}
			})

			return nil
		},
	}

	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (event stream)")

	return cmd
}

// statusHeaderLines renders the labeled lines `citeck status` prints above
// the app table. A line whose value is empty is omitted (the License: line on
// an older daemon, Deps:/State:/Content storage: on a namespace with nothing to
// report).
//
// Labels are padded to the width of the longest one PRINTED, so the values
// line up whichever lines a namespace has: "Content storage:" appears on S3
// namespaces only, and widening every other namespace's header for it would
// change the output of all of them. padRight (shared with the install wizard)
// is CJK-aware — though labels are ASCII here, using the same helper keeps
// alignment logic consistent.
func statusHeaderLines(ns *api.NamespaceDto, licStatus *api.LicenseStatusDto, depsDto *api.DependenciesDto) []string {
	type row struct{ label, value string }
	rows := []row{
		{"Name:", ns.Name},
		{"Status:", output.ColorizeStatus(ns.Status)},
		{"Bundle:", ns.BundleRef},
		{"License:", formatLicenseLine(licStatus)},
		{"Deps:", dependencyHintLine(ns, depsDto)},
		// A store that keeps refusing is otherwise invisible here: the
		// per-app commands answered success (the actions did succeed) and the
		// only trace is one WARN in the daemon log.
		{"State:", stateWriteStatusLine(ns)},
		{"Content storage:", contentStorageStatusLine(ns)},
	}
	for _, link := range ns.Links {
		if link.Name == "Citeck UI" {
			rows = append(rows, row{"URL:", link.URL})
			break
		}
	}
	// Never narrower than "License:", the width every label had before the
	// content-storage line existed, so a DB namespace's header is unchanged.
	width := displayWidth("License:")
	kept := rows[:0]
	for _, r := range rows {
		// Name and Status are always printed, even empty: they are the
		// header's frame, not an optional report.
		if r.value == "" && r.label != "Name:" && r.label != "Status:" {
			continue
		}
		kept = append(kept, r)
		width = max(width, displayWidth(r.label))
	}
	lines := make([]string, 0, len(kept))
	for _, r := range kept {
		lines = append(lines, fmt.Sprintf("%s  %s", output.Colorize(output.Bold, padRight(r.label, width)), r.value))
	}
	return lines
}

// formatLicenseLine renders the value of the "License:" status line.
// Returns "" when st is nil — the daemon predates the licenses/status
// endpoint (or it errored), so the line is omitted entirely.
func formatLicenseLine(st *api.LicenseStatusDto) string {
	switch {
	case st == nil:
		return ""
	case st.Enterprise:
		line := t("cli.license.enterprise", "tenant", st.Tenant, "date", st.ValidUntil)
		if st.ExpiringSoon {
			line += " " + output.Colorize(output.Yellow,
				t("cli.license.expiresSoon", "days", strconv.Itoa(st.DaysLeft)))
		}
		return line
	case st.Tenant != "":
		// Records exist but none validates — expired (or otherwise invalid).
		return output.Colorize(output.Red,
			t("cli.license.expired", "tenant", st.Tenant, "date", st.ValidUntil))
	default:
		return t("cli.license.community")
	}
}

func watchEvents(c *client.DaemonClient) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	tty := output.IsTTY()
	var lastLines int

	// render fetches the current namespace and redraws the live table.
	// Text is always terminated with a trailing newline so the cursor
	// ends at the start of the line BELOW the last row — ClearLines(n)
	// then clears exactly n lines of previously-printed content.
	render := func() {
		ns, fetchErr := c.GetNamespace()
		if fetchErr != nil {
			return
		}
		if output.IsJSON() {
			output.PrintJSON(ns)
			return
		}

		urlLine := ""
		for _, link := range ns.Links {
			if link.Name == "Citeck UI" {
				urlLine = fmt.Sprintf("%s  %s\n", output.Colorize(output.Bold, padRight("URL:", 7)), link.URL)
				break
			}
		}
		header := fmt.Sprintf("%s  %s\n%s  %s\n%s  %s\n%s",
			output.Colorize(output.Bold, padRight("Name:", 7)), ns.Name,
			output.Colorize(output.Bold, padRight("Status:", 7)), output.ColorizeStatus(ns.Status),
			output.Colorize(output.Bold, padRight("Bundle:", 7)), ns.BundleRef,
			urlLine)
		r := output.FormatAppTable(ns.Apps)
		table := r.Table
		if r.AnyEdited {
			table += "\n" + output.Colorize(output.Dim, editedLegend)
		}

		if tty && lastLines > 0 {
			output.ClearLines(lastLines)
		}

		text := header + "\n" + table + "\n"
		fmt.Print(text) //nolint:forbidigo // CLI live output
		lastLines = strings.Count(text, "\n")
	}

	// Initial render so the user sees output immediately; subsequent
	// renders are driven by the event stream below.
	render()

	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}

	for range events {
		render()
	}

	return nil
}
