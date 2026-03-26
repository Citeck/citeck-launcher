package cli

import (
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var foreground bool
	var desktop bool
	var noUI bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon and namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if desktop {
				config.SetDesktopMode(true)
			}
			return daemon.Start(daemon.StartOptions{
				Foreground: foreground,
				NoUI:       noUI,
			})
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Desktop mode: use ~/.citeck/launcher/ and workspace structure")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "Disable Web UI (CLI-only, Unix socket only)")

	return cmd
}
