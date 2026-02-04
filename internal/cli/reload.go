package cli

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Hot-reload namespace configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(clientOpts())
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
			if !result.Success {
				return exitWithCode(ExitError, "reload failed: %s", result.Message)
			}
			return nil
		},
	}
}
