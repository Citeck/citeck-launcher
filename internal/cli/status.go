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
	var apps bool

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
				output.PrintText("%s  %s", output.Colorize(output.Bold, "Name:"), ns.Name)
				output.PrintText("%s  %s", output.Colorize(output.Bold, "Status:"), output.ColorizeStatus(ns.Status))
				if ns.BundleRef != "" {
					output.PrintText("%s  %s", output.Colorize(output.Bold, "Bundle:"), ns.BundleRef)
				}
				for _, link := range ns.Links {
					if link.Name == "ECOS UI" {
						output.PrintText("%s  %s", output.Colorize(output.Bold, "URL:"), link.URL)
						break
					}
				}

				if (apps || len(ns.Apps) > 0) && len(ns.Apps) > 0 {
					fmt.Println()
					r := output.FormatAppTable(ns.Apps)
					output.PrintText(r.Table)
				}
			})

			return nil
		},
	}

	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (event stream)")
	cmd.Flags().BoolVarP(&apps, "apps", "a", false, "Show app details")

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
			if link.Name == "ECOS UI" {
				urlLine = fmt.Sprintf("%s  %s\n", output.Colorize(output.Bold, "URL:"), link.URL)
				break
			}
		}
		header := fmt.Sprintf("%s  %s\n%s  %s\n%s  %s\n%s",
			output.Colorize(output.Bold, "Name:"), ns.Name,
			output.Colorize(output.Bold, "Status:"), output.ColorizeStatus(ns.Status),
			output.Colorize(output.Bold, "Bundle:"), ns.BundleRef,
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
