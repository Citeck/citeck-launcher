package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	var detach bool
	var timeout int

	cmd := &cobra.Command{
		Use:   "restart [app]",
		Short: "Restart an app or the entire namespace",
		Long: "Restart a single app, or the entire namespace if no app specified (stop → start).\n" +
			"Waits for RUNNING by default. Use -d/--detach to skip waiting.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// No app → restart entire namespace (stop + start)
			if len(args) == 0 {
				return restartNamespace(c, time.Duration(timeout)*time.Second, detach)
			}

			appName := args[0]
			result, err := c.RestartApp(appName)
			if err != nil {
				return fmt.Errorf("restart %q: %w", appName, err)
			}

			output.PrintResult(result, func() {
				output.PrintText(result.Message)
			})

			// Fire-and-forget in --detach, JSON output, or non-TTY (scripts).
			if detach || output.IsJSON() || !output.IsTTY() {
				return nil
			}

			if waitErr := streamSingleAppStatus(c, appName); waitErr != nil {
				if errors.Is(waitErr, errInterrupted) {
					return nil
				}
				return waitErr
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Return immediately without waiting for RUNNING")
	cmd.Flags().IntVar(&timeout, "timeout", 300, "Timeout in seconds for the namespace-stop phase (full restart only)")

	return cmd
}

func restartNamespace(c *client.DaemonClient, stopTimeout time.Duration, detach bool) error {
	output.PrintText("Stopping namespace...")
	if _, stopErr := c.StopNamespace(); stopErr != nil {
		return fmt.Errorf("stop namespace: %w", stopErr)
	}
	if waitErr := waitForStopped(c, stopTimeout); waitErr != nil {
		return fmt.Errorf("waiting for stop: %w", waitErr)
	}
	output.PrintText("Namespace stopped")

	output.PrintText("Starting namespace...")
	if _, startErr := c.StartNamespace(); startErr != nil {
		return fmt.Errorf("start namespace: %w", startErr)
	}
	output.PrintText("Namespace start requested")
	if detach {
		return nil
	}
	if waitErr := streamLiveStatus(c, liveStatusOpts{}); waitErr != nil {
		if errors.Is(waitErr, errInterrupted) {
			return nil
		}
		return waitErr
	}
	return nil
}
