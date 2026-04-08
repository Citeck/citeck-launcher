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
			c := client.TryNew(clientOpts())
			if c == nil {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText("Platform is not running")
				}
				return nil
			}
			defer c.Close()

			if !c.IsRunning() {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText("Platform is not running")
				}
				return nil
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

				if (apps || len(ns.Apps) > 0) && len(ns.Apps) > 0 {
					fmt.Println()
					headers := []string{"APP", "STATUS", "IMAGE", "CPU", "MEMORY"}
					var rows [][]string
					for _, app := range ns.Apps {
						rows = append(rows, []string{
							app.Name,
							output.ColorizeStatus(app.Status),
							app.Image,
							app.CPU,
							app.Memory,
						})
					}
					output.PrintText(output.FormatTable(headers, rows))
				}
			})

			if watch {
				return watchEvents(c)
			}

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
	go func() {
		<-sigCh
		cancel()
	}()

	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}

	tty := isTTYOut()
	var lastLines int

	for range events {
		// Re-fetch full status on every event and redraw
		ns, fetchErr := c.GetNamespace()
		if fetchErr != nil {
			continue
		}

		if output.IsJSON() {
			output.PrintJSON(ns)
			continue
		}

		// Build output
		header := fmt.Sprintf("%s  %s\n%s  %s\n%s  %s\n",
			output.Colorize(output.Bold, "Name:"), ns.Name,
			output.Colorize(output.Bold, "Status:"), output.ColorizeStatus(ns.Status),
			output.Colorize(output.Bold, "Bundle:"), ns.BundleRef)

		table, _, _, _ := renderAppTable(ns.Apps)

		// TTY: clear previous output and redraw
		if tty && lastLines > 0 {
			clearLines(lastLines)
		}

		text := header + "\n" + table
		fmt.Print(text) //nolint:forbidigo // CLI live output
		lastLines = strings.Count(text, "\n") + 1
	}

	return nil
}
