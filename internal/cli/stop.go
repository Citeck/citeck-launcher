package cli

import (
	"fmt"
	"os"
	"sort"
	"time"

	"golang.org/x/term"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var shutdown bool

	cmd := &cobra.Command{
		Use:   "stop [app]",
		Short: "Stop the namespace (or a single app)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(clientOpts())
			if c == nil || !c.IsRunning() {
				output.PrintText("Platform is not running")
				return nil // idempotent by design
			}
			defer c.Close()

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

			// No app → stop namespace and show live status
			result, err := c.StopNamespace()
			if err != nil {
				return fmt.Errorf("stop namespace: %w", err)
			}
			output.PrintText("%s", result.Message)

			if err := streamStopStatus(c); err != nil {
				return err
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

	return cmd
}

// streamStopStatus polls the daemon and shows live stop progress until all apps are STOPPED.
func streamStopStatus(c *client.DaemonClient) error {
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	firstPrint := true
	linesPrinted := 0
	deadline := time.Now().Add(5 * time.Minute)

	for time.Now().Before(deadline) {
		ns, err := c.GetNamespace()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		apps := ns.Apps
		sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

		// Clear previous output (only in TTY mode)
		if isTTY && !firstPrint && linesPrinted > 0 {
			for i := 0; i < linesPrinted; i++ {
				fmt.Print("\033[A\033[2K") //nolint:forbidigo // ANSI clear
			}
		}
		firstPrint = false

		lines := 0
		allStopped := true
		stopped := 0

		for _, app := range apps {
			status := app.Status
			marker := "●"
			if status == "STOPPED" {
				marker = "○"
				stopped++
			} else {
				allStopped = false
			}
			fmt.Printf("  %s %-30s %s\n", marker, app.Name, status) //nolint:forbidigo // CLI table
			lines++
		}
		fmt.Printf("\n  %d/%d stopped\n", stopped, len(apps)) //nolint:forbidigo // CLI summary
		lines += 2
		linesPrinted = lines

		if allStopped || ns.Status == "STOPPED" {
			fmt.Printf("\nAll %d apps stopped.\n", len(apps)) //nolint:forbidigo // CLI success
			return nil
		}

		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for namespace to stop")
}
