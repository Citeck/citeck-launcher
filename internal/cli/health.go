package cli

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// healthBanner returns the banner text and ANSI color for a given exit code.
// The contract: the banner the user sees MUST match the exit code. Any new
// exit code must extend this table rather than be handled inline, so a
// regression test can keep them in sync.
func healthBanner(exitCode int) (label, color string) {
	switch exitCode {
	case ExitOK:
		return "HEALTHY", output.Green
	case ExitDaemonNotRunning, ExitError:
		// ExitError is what the "connect to daemon" branch currently
		// bubbles up through root.Execute; treat it as "daemon down" for
		// banner purposes so the user sees a truthful label.
		return "DAEMON DOWN", output.Red
	case ExitUnhealthy:
		return "UNHEALTHY", output.Red
	default:
		return "UNHEALTHY", output.Red
	}
}

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "System health check (exit 0=healthy, 1=daemon down, 8=unhealthy)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"healthy": false, "error": err.Error(), "status": "daemon_down"})
				} else {
					label, color := healthBanner(ExitDaemonNotRunning)
					output.PrintText("Status: %s", output.Colorize(color, label))
				}
				return exitWithCode(ExitDaemonNotRunning, "connect to daemon: %v", err)
			}
			defer c.Close()

			health, err := c.GetHealth()
			if err != nil {
				if !output.IsJSON() {
					label, color := healthBanner(ExitDaemonNotRunning)
					output.PrintText("Status: %s", output.Colorize(color, label))
				}
				return fmt.Errorf("get health: %w", err)
			}

			// Decide the authoritative exit code first, then derive the banner
			// from it. This keeps the text output in lockstep with the exit
			// contract (0=healthy, 1=daemon down, 8=unhealthy) — fixes B7-03
			// where a green connection could produce a mismatched banner.
			exitCode := ExitOK
			if !health.Healthy {
				exitCode = ExitUnhealthy
			}

			output.PrintResult(health, func() {
				renderHealth(health, exitCode)
			})

			if exitCode != ExitOK {
				return exitWithCode(exitCode, "system is unhealthy")
			}
			return nil
		},
	}
}

// renderHealth prints the banner + per-check list. Extracted so tests can
// exercise the banner-vs-exit-code contract without round-tripping through
// cobra.
func renderHealth(health *api.HealthDto, exitCode int) {
	label, color := healthBanner(exitCode)
	output.PrintText("Status: %s", output.Colorize(color, label))
	fmt.Println()

	if health == nil {
		return
	}
	for _, check := range health.Checks {
		icon := formatCheckIcon(check.Status)
		msg := check.Name
		if check.Message != "" {
			msg += " — " + check.Message
		}
		output.PrintText("  %s  %s", icon, msg)
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
