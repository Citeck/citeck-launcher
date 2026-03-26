package cli

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <app>",
		Short: "Restart an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			c, err := client.New(flagHost, flagToken)
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
			return nil
		},
	}
}
