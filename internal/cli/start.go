package cli

import (
	"github.com/niceteck/citeck-launcher/internal/daemon"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon and namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.Start(foreground)
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground")

	return cmd
}
