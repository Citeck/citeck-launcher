package cli

import (
	"fmt"

	"github.com/niceteck/citeck-launcher/internal/client"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var shutdown bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(flagHost, flagToken)
			if c == nil || !c.IsRunning() {
				output.PrintText("Platform is not running")
				return nil
			}
			defer c.Close()

			result, err := c.StopNamespace()
			if err != nil {
				return fmt.Errorf("stop namespace: %w", err)
			}
			output.PrintResult(result, func() {
				output.PrintText(result.Message)
			})

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

	return cmd
}
