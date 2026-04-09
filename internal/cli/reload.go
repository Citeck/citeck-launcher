package cli

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newReloadCmd() *cobra.Command {
	var noWait bool

	cmd := &cobra.Command{
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

			if !result.Success {
				return exitWithCode(ExitError, "reload failed: %s", result.Message)
			}

			output.PrintText(result.Message)

			if noWait {
				return nil
			}

			// Wait for all services to stabilize.
			if waitErr := StreamReloadStatus(c); waitErr != nil {
				if errors.Is(waitErr, errInterrupted) {
					return nil // Changes apply in background.
				}
				return waitErr
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&noWait, "no-wait", "d", false, "Don't wait for services to stabilize (detach)")
	return cmd
}
