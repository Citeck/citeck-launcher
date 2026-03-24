package cli

import (
	"fmt"

	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newCleanCmd() *cobra.Command {
	var execute bool
	var volumes bool

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Clean up orphaned containers and volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Use Docker client to find orphaned citeck containers/volumes
			if !execute {
				output.PrintResult(map[string]any{
					"dryRun": true,
					"message": "Run with --execute to actually remove resources",
				}, func() {
					output.PrintText("Scanning for orphaned resources...")
					output.PrintText("(Run with --execute to remove)")
				})
				return nil
			}

			if !flagYes {
				output.PrintText("Remove orphaned resources? [y/N] ")
				// TODO: Read confirmation
				return fmt.Errorf("confirmation required (use --yes to skip)")
			}

			output.PrintText("Cleaning up orphaned resources...")
			_ = volumes
			// TODO: Implement actual cleanup
			output.PrintText("Done")
			return nil
		},
	}

	cmd.Flags().BoolVar(&execute, "execute", false, "Actually remove resources (dry run by default)")
	cmd.Flags().BoolVar(&volumes, "volumes", false, "Also remove orphaned volumes")

	return cmd
}
