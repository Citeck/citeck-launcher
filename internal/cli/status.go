package cli

import (
	"fmt"
	"os"

	"github.com/niceteck/citeck-launcher/internal/client"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var watch bool
	var apps bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show namespace and app status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(flagHost, flagToken)
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
	// TODO: WebSocket event streaming (Phase 3+)
	fmt.Fprintln(os.Stderr, "Event streaming not yet implemented")
	return nil
}
