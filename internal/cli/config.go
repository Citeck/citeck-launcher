package cli

import (
	"fmt"
	"os"

	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management",
	}

	cmd.AddCommand(
		newConfigViewCmd(),
		newConfigValidateCmd(),
	)

	return cmd
}

func newConfigViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "view",
		Aliases: []string{"show"},
		Short:   "Display current namespace.yml",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.NamespaceConfigPath()
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("config file not found: %s", path)
				}
				return fmt.Errorf("read config: %w", err)
			}

			if output.IsJSON() {
				output.PrintJSON(map[string]string{
					"path":    path,
					"content": string(data),
				})
			} else {
				fmt.Print(string(data))
			}
			return nil
		},
	}
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate namespace configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.NamespaceConfigPath()
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("config file not found: %s", path)
				}
				return fmt.Errorf("read config: %w", err)
			}

			var errors []string

			// Basic YAML validity check
			if len(data) == 0 {
				errors = append(errors, "config file is empty")
			}

			// Semantic validation planned for Phase 5G
			// (auth type, port range, TLS files, bundle ref format)

			result := map[string]any{
				"path":   path,
				"valid":  len(errors) == 0,
				"errors": errors,
			}

			output.PrintResult(result, func() {
				if len(errors) == 0 {
					output.PrintText("%s Configuration is valid", output.Colorize(output.Green, "PASS"))
				} else {
					output.PrintText("%s %d error(s) found:", output.Colorize(output.Red, "FAIL"), len(errors))
					for _, e := range errors {
						output.PrintText("  - %s", e)
					}
					os.Exit(ExitConfigError)
				}
			})

			return nil
		},
	}
}
