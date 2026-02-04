package cli

import (
	"fmt"
	"os"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var daemonFlag bool

	cmd := &cobra.Command{
		Use:   "validate [file]",
		Short: "Validate namespace or daemon configuration",
		Long:  "Validates a namespace.yml configuration file. If no file is given, validates the active namespace config. Use --daemon to also validate daemon.yml.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hasErrors := false

			// Validate namespace config
			var nsPath string
			if len(args) > 0 {
				nsPath = args[0]
			} else {
				nsPath = config.ResolveNamespaceConfigPath("daemon", "default")
			}

			switch _, err := os.Stat(nsPath); {
			case err != nil && os.IsNotExist(err):
				output.PrintText("namespace config not found: %s", nsPath)
				hasErrors = true
			case err != nil:
				return fmt.Errorf("stat %s: %w", nsPath, err)
			default:
				if _, loadErr := namespace.LoadNamespaceConfig(nsPath); loadErr != nil {
					output.PrintText("namespace config INVALID: %s", nsPath)
					output.PrintText("  %v", loadErr)
					hasErrors = true
				} else {
					output.PrintText("namespace config OK: %s", nsPath)
				}
			}

			// Validate daemon config
			if daemonFlag {
				_, err := config.LoadDaemonConfig()
				if err != nil {
					output.PrintText("daemon config INVALID: %s", config.DaemonConfigPath())
					output.PrintText("  %v", err)
					hasErrors = true
				} else {
					output.PrintText("daemon config OK: %s", config.DaemonConfigPath())
				}
			}

			if hasErrors {
				return ExitCodeError{Code: 1, Err: fmt.Errorf("validation failed")}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Also validate daemon.yml")

	return cmd
}
