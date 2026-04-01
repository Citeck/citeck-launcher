package cli

import (
	"errors"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStartCmd(version string) *cobra.Command {
	var foreground bool
	var desktop bool
	var noUI bool

	cmd := &cobra.Command{
		Use:   "start [app]",
		Short: "Start the daemon and namespace (or a single app)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// If daemon is already running, send start command
			if c := client.TryNew(clientOpts()); c != nil {
				defer c.Close()

				// App specified → start single app
				if len(args) == 1 {
					appName := args[0]
					result, err := c.StartApp(appName)
					if err != nil {
						return fmt.Errorf("start %q: %w", appName, err)
					}
					output.PrintResult(result, func() {
						output.PrintText(result.Message)
					})
					return nil
				}

				// No app → start namespace
				result, err := c.StartNamespace()
				if err != nil {
					return fmt.Errorf("start namespace: %w", err)
				}
				output.PrintResult(result, func() {
					output.PrintText("%s", result.Message)
				})
				return nil
			}

			// Daemon not running → start daemon
			if len(args) == 1 {
				return fmt.Errorf("daemon is not running — start it first with 'citeck start'")
			}

			err := daemon.Start(daemon.StartOptions{
				Foreground: foreground,
				Desktop:    desktop,
				NoUI:       noUI,
				Version:    version,
			})
			if errors.Is(err, daemon.ErrShutdownRequested) {
				return nil // clean shutdown, not an error
			}
			return err
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Desktop mode: use ~/.citeck/launcher/ and workspace structure")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "Disable Web UI (CLI-only, Unix socket only)")

	return cmd
}
