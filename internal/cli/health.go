package cli

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "System health check",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"healthy": false, "error": err.Error()})
				}
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			health, err := c.GetHealth()
			if err != nil {
				return fmt.Errorf("get health: %w", err)
			}

			output.PrintResult(health, func() {
				if health.Healthy {
					output.PrintText("Status: %s", output.Colorize(output.Green, "HEALTHY"))
				} else {
					output.PrintText("Status: %s", output.Colorize(output.Red, "UNHEALTHY"))
				}
				fmt.Println()

				for _, check := range health.Checks {
					icon := formatCheckIcon(check.Status)
					msg := check.Name
					if check.Message != "" {
						msg += " — " + check.Message
					}
					output.PrintText("  %s  %s", icon, msg)
				}
			})

			if !health.Healthy {
				return exitWithCode(ExitUnhealthy, "system is unhealthy")
			}
			return nil
		},
	}
}

func formatCheckIcon(status string) string {
	switch status {
	case "ok":
		return output.Colorize(output.Green, "[OK]   ")
	case "warning":
		return output.Colorize(output.Yellow, "[WARN] ")
	case "error":
		return output.Colorize(output.Red, "[ERROR]")
	default:
		return "[?]    "
	}
}
