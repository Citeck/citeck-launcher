package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

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

			output.PrintResult(ns, func() {
				// Pad labels to the width of the longest ("Status:"/"Bundle:" = 7)
				// so the values line up visually. padRight (shared with the
				// install wizard) is CJK-aware — though labels are ASCII here,
				// using the same helper keeps alignment logic consistent.
				output.PrintText("%s  %s", output.Colorize(output.Bold, padRight("Name:", 7)), ns.Name)
				output.PrintText("%s  %s", output.Colorize(output.Bold, padRight("Status:", 7)), output.ColorizeStatus(ns.Status))
				if ns.BundleRef != "" {
					output.PrintText("%s  %s", output.Colorize(output.Bold, padRight("Bundle:", 7)), ns.BundleRef)
				}
				for _, link := range ns.Links {
					if link.Name == "Citeck UI" {
						output.PrintText("%s  %s", output.Colorize(output.Bold, padRight("URL:", 7)), link.URL)
						break
					}
				}

				if len(ns.Apps) > 0 {
					fmt.Println()
					r := output.FormatAppTable(ns.Apps)
					output.PrintText(r.Table)
				}
			})

			return nil
		},
	}

	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (event stream)")

	return cmd
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
		table, _, _, _ := renderAppTable(ns.Apps)

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
