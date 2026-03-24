package cli

import (
	"fmt"
	"strings"

	"github.com/niceteck/citeck-launcher/internal/client"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <app>",
		Short: "Show detailed information about an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			c, err := client.New(flagHost, flagToken)
			if err != nil {
				return err
			}
			defer c.Close()

			inspect, err := c.InspectApp(appName)
			if err != nil {
				return fmt.Errorf("describe app %q: %w", appName, err)
			}

			output.PrintResult(inspect, func() {
				containerID := inspect.ContainerID
				if len(containerID) > 12 {
					containerID = containerID[:12]
				}

				pairs := [][2]string{
					{"Name", inspect.Name},
					{"Container ID", containerID},
					{"Image", inspect.Image},
					{"Status", output.ColorizeStatus(inspect.Status)},
					{"State", inspect.State},
					{"Network", inspect.Network},
					{"Started at", inspect.StartedAt},
					{"Uptime", formatUptime(inspect.Uptime)},
					{"Restarts", fmt.Sprintf("%d", inspect.RestartCount)},
				}
				output.PrintText(output.FormatKeyValue(pairs))

				if len(inspect.Ports) > 0 {
					fmt.Println()
					output.PrintText(output.Colorize(output.Bold, "Ports:"))
					for _, p := range inspect.Ports {
						output.PrintText("  %s", p)
					}
				}
				if len(inspect.Volumes) > 0 {
					fmt.Println()
					output.PrintText(output.Colorize(output.Bold, "Volumes:"))
					for _, v := range inspect.Volumes {
						output.PrintText("  %s", v)
					}
				}
				if len(inspect.Env) > 0 {
					fmt.Println()
					output.PrintText(output.Colorize(output.Bold, "Environment:"))
					for _, e := range inspect.Env {
						output.PrintText("  %s", e)
					}
				}
			})

			return nil
		},
	}
}

func formatUptime(ms int64) string {
	if ms <= 0 {
		return "—"
	}

	seconds := ms / 1000
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
		parts = append(parts, fmt.Sprintf("%dh", hours%24))
		parts = append(parts, fmt.Sprintf("%dm", minutes%60))
	} else if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		parts = append(parts, fmt.Sprintf("%dm", minutes%60))
		parts = append(parts, fmt.Sprintf("%ds", seconds%60))
	} else if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
		parts = append(parts, fmt.Sprintf("%ds", seconds%60))
	} else {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	return strings.Join(parts, " ")
}
