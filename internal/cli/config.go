package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
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
			data, err := os.ReadFile(path) //nolint:gosec // G304: path is from internal config
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
			data, err := os.ReadFile(path) //nolint:gosec // G304: path is from internal config
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

			// Parse and run semantic validation
			cfg, parseErr := namespace.ParseNamespaceConfig(data)
			if parseErr != nil {
				errors = append(errors, fmt.Sprintf("YAML parse error: %s", parseErr.Error()))
			} else {
				errors = append(errors, validateNamespaceConfig(cfg)...)
			}

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
				}
			})

			if len(errors) > 0 {
				return exitWithCode(ExitConfigError, "%d config error(s)", len(errors))
			}
			return nil
		},
	}
}

// validateNamespaceConfig performs semantic validation of a namespace config.
func validateNamespaceConfig(cfg *namespace.Config) []string {
	var errors []string

	// Auth type
	switch cfg.Authentication.Type {
	case namespace.AuthBasic, namespace.AuthKeycloak:
		// valid
	default:
		errors = append(errors, fmt.Sprintf("invalid authentication.type: %q (expected BASIC or KEYCLOAK)", cfg.Authentication.Type))
	}

	// Users required for BASIC auth
	if cfg.Authentication.Type == namespace.AuthBasic && len(cfg.Authentication.Users) == 0 {
		errors = append(errors, "authentication.users must not be empty when type=BASIC")
	}

	// Port range
	if cfg.Proxy.Port < 1 || cfg.Proxy.Port > 65535 {
		errors = append(errors, fmt.Sprintf("proxy.port %d out of range (1-65535)", cfg.Proxy.Port))
	}

	// TLS cert/key files exist (only check for manual TLS, not Let's Encrypt)
	errors = append(errors, validateTLSFiles(cfg)...)

	// BundleRef format
	if !cfg.BundleRef.IsEmpty() {
		ref := cfg.BundleRef.String()
		if !strings.Contains(ref, ":") {
			errors = append(errors, fmt.Sprintf("invalid bundleRef format: %q (expected repo:version)", ref))
		}
	}

	return errors
}

func validateTLSFiles(cfg *namespace.Config) []string {
	if !cfg.Proxy.TLS.Enabled || cfg.Proxy.TLS.LetsEncrypt {
		return nil
	}
	var errs []string
	if cfg.Proxy.TLS.CertPath != "" {
		if _, err := os.Stat(cfg.Proxy.TLS.CertPath); err != nil { //nolint:gosec // G703: path from namespace config file, not user input
			errs = append(errs, fmt.Sprintf("TLS cert file not found: %s", cfg.Proxy.TLS.CertPath))
		}
	}
	if cfg.Proxy.TLS.KeyPath != "" {
		if _, err := os.Stat(cfg.Proxy.TLS.KeyPath); err != nil { //nolint:gosec // G703: path from namespace config file, not user input
			errs = append(errs, fmt.Sprintf("TLS key file not found: %s", cfg.Proxy.TLS.KeyPath))
		}
	}
	return errs
}
