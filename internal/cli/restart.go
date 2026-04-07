package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	var wait bool
	var timeout int

	cmd := &cobra.Command{
		Use:   "restart [app]",
		Short: "Restart an app or the entire namespace",
		Long:  "Restart a single app, or the entire namespace if no app specified (stop → start).",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// No app → restart entire namespace (stop + start)
			if len(args) == 0 {
				return restartNamespace(c)
			}

			appName := args[0]
			result, err := c.RestartApp(appName)
			if err != nil {
				return fmt.Errorf("restart %q: %w", appName, err)
			}

			output.PrintResult(result, func() {
				output.PrintText(result.Message)
			})

			if wait {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
				defer cancel()
				events, sseErr := c.StreamEvents(ctx)
				if sseErr != nil {
					return fmt.Errorf("connect to event stream: %w", sseErr)
				}
				for {
					select {
					case <-ctx.Done():
						return exitWithCode(ExitTimeout, "timeout waiting for %s to restart", appName)
					case evt, ok := <-events:
						if !ok {
							return nil
						}
						if evt.Type == "app_status" && evt.AppName == appName && evt.After == "RUNNING" {
							output.PrintText("App %s: RUNNING", appName)
							return nil
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for app to reach RUNNING status")
	cmd.Flags().IntVar(&timeout, "timeout", 300, "Timeout in seconds (with --wait)")

	return cmd
}

func restartNamespace(c *client.DaemonClient) error {
	output.PrintText("Stopping namespace...")
	if _, stopErr := c.StopNamespace(); stopErr != nil {
		return fmt.Errorf("stop namespace: %w", stopErr)
	}
	if waitErr := waitForStopped(c, 120*time.Second); waitErr != nil {
		return fmt.Errorf("waiting for stop: %w", waitErr)
	}
	output.PrintText("Namespace stopped")

	output.PrintText("Starting namespace...")
	if _, startErr := c.StartNamespace(); startErr != nil {
		return fmt.Errorf("start namespace: %w", startErr)
	}
	output.PrintText("Namespace start requested")
	return streamLiveStatus(c, false)
}
