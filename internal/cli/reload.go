package cli

import (
	"fmt"

	"github.com/niceteck/citeck-launcher/internal/client"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Hot-reload namespace configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(flagHost, flagToken)
			if c == nil || !c.IsRunning() {
				output.PrintText("Platform is not running")
				return nil
			}
			defer c.Close()

			result, err := c.ReloadNamespace()
			if err != nil {
				return fmt.Errorf("reload: %w", err)
			}

			output.PrintResult(result, func() {
				if result.Success {
					output.PrintText(result.Message)
				} else {
					output.PrintText("Reload failed: %s", result.Message)
				}
			})
			return nil
		},
	}
}
