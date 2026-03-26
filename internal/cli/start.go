package cli

import (
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStartCmd(version string) *cobra.Command {
	var foreground bool
	var desktop bool
	var noUI bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon and namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If daemon is already running, send namespace start command
			if c := client.TryNew(flagHost, flagToken); c != nil {
				defer c.Close()
				result, err := c.StartNamespace()
				if err != nil {
					return err
				}
				output.PrintResult(result, func() {
					output.PrintText("%s", result.Message)
				})
				return nil
			}

			if desktop {
				config.SetDesktopMode(true)
			}
			return daemon.Start(daemon.StartOptions{
				Foreground: foreground,
				NoUI:       noUI,
				Version:    version,
			})
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Desktop mode: use ~/.citeck/launcher/ and workspace structure")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "Disable Web UI (CLI-only, Unix socket only)")

	return cmd
}
