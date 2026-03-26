package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var shutdown bool
	var wait bool
	var timeout int

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(flagHost, flagToken)
			if c == nil || !c.IsRunning() {
				output.PrintText("Platform is not running")
				return nil // idempotent by design
			}
			defer c.Close()

			result, err := c.StopNamespace()
			if err != nil {
				return fmt.Errorf("stop namespace: %w", err)
			}
			output.PrintResult(result, func() {
				output.PrintText(result.Message)
			})

			if wait {
				if err := waitForStop(c, timeout); err != nil {
					return err
				}
			}

			if shutdown {
				r, err := c.Shutdown()
				if err != nil {
					return fmt.Errorf("shutdown daemon: %w", err)
				}
				output.PrintResult(r, func() {
					output.PrintText(r.Message)
				})
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&shutdown, "shutdown", "s", false, "Also shutdown the daemon")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for namespace to stop")
	cmd.Flags().IntVar(&timeout, "timeout", 120, "Timeout in seconds (with --wait)")

	return cmd
}

func waitForStop(c *client.DaemonClient, timeoutSec int) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return exitWithCode(ExitTimeout, "timeout waiting for stop")
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			if evt.Type == "namespace_status" && evt.After == "STOPPED" {
				output.PrintText("Namespace stopped")
				return nil
			}
		}
	}
}
