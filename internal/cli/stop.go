package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var shutdown bool
	var detach bool
	var leaveRunning bool

	cmd := &cobra.Command{
		Use:   "stop [app]",
		Short: "Stop the namespace (or a single app)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if leaveRunning && !shutdown {
				return fmt.Errorf("--leave-running requires --shutdown")
			}
			if leaveRunning && len(args) == 1 {
				return fmt.Errorf("--leave-running cannot be combined with an app argument")
			}

			c := client.TryNew(clientOpts())
			if c == nil || !c.IsRunning() {
				output.PrintText("Platform is not running")
				return nil // idempotent by design
			}
			defer c.Close()

			// --shutdown --leave-running: detach the daemon without touching
			// containers. Used by install.sh for binary upgrades — the next
			// daemon adopts the running platform via doStart hash matching.
			if leaveRunning {
				r, shutdownErr := c.ShutdownLeaveRunning()
				if shutdownErr != nil {
					return fmt.Errorf("detach daemon: %w", shutdownErr)
				}
				output.PrintResult(r, func() {
					output.PrintText(r.Message)
				})
				return nil
			}

			// App specified → stop single app
			if len(args) == 1 {
				appName := args[0]
				result, err := c.StopApp(appName)
				if err != nil {
					return fmt.Errorf("stop %q: %w", appName, err)
				}
				output.PrintResult(result, func() {
					output.PrintText(result.Message)
				})
				return nil
			}

			// No app → stop namespace
			result, err := c.StopNamespace()
			if err != nil {
				return fmt.Errorf("stop namespace: %w", err)
			}
			output.PrintText("%s", result.Message)

			if !detach {
				if stopErr := streamStopStatus(c); stopErr != nil {
					return stopErr
				}
			}

			if shutdown {
				r, shutdownErr := c.Shutdown()
				if shutdownErr != nil {
					return fmt.Errorf("shutdown daemon: %w", shutdownErr)
				}
				output.PrintResult(r, func() {
					output.PrintText(r.Message)
				})
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&shutdown, "shutdown", "s", false, "Also shutdown the daemon")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Send stop and return without waiting (like docker-compose stop -d)")
	cmd.Flags().BoolVar(&leaveRunning, "leave-running", false, "With --shutdown: exit the daemon without stopping containers (for binary upgrades)")

	return cmd
}

// streamStopStatus polls the daemon and shows live stop progress until all apps are STOPPED.
func streamStopStatus(c *client.DaemonClient) error {
	isTTY := output.IsTTY()
	linesPrinted := 0
	lastStopped := -1
	deadline := time.Now().Add(5 * time.Minute)

	for time.Now().Before(deadline) {
		ns, err := c.GetNamespace()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		r := output.FormatAppTable(ns.Apps)
		table, total := r.Table, r.Total
		stopped := 0
		for _, app := range ns.Apps {
			if app.Status == "STOPPED" {
				stopped++
			}
		}

		if isTTY {
			if linesPrinted > 0 {
				output.ClearLines(linesPrinted)
			}
			fmt.Println(table)            //nolint:forbidigo // CLI table
			fmt.Println()                 //nolint:forbidigo // CLI spacing
			fmt.Printf("  %d/%d stopped\n", stopped, total) //nolint:forbidigo // CLI summary
			linesPrinted = strings.Count(table, "\n") + 3
		} else if stopped != lastStopped {
			fmt.Printf("  %d/%d stopped\n", stopped, total) //nolint:forbidigo // CLI progress
		}
		lastStopped = stopped

		if stopped == total || ns.Status == "STOPPED" {
			fmt.Printf("\nAll %d apps stopped.\n", total) //nolint:forbidigo // CLI success
			return nil
		}

		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for namespace to stop")
}
