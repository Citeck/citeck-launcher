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
		Use:   "restart <app>",
		Short: "Restart an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			c, err := client.New(clientOpts())
			if err != nil {
				return err
			}
			defer c.Close()

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
