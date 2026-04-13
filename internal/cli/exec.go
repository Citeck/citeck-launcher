package cli

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <app> -- <command...>",
		Short: "Execute command in container",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("app name is required. Usage: citeck exec <app> -- <command...>")
			}
			if len(args) < 2 {
				return fmt.Errorf("command is required. Usage: citeck exec <app> -- <command...>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			command := args[1:]

			// Strip "--" separator if present
			if len(command) > 0 && command[0] == "--" {
				command = command[1:]
			}
			if len(command) == 0 {
				return fmt.Errorf("command is required")
			}

			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			result, err := c.ExecApp(appName, command)
			if err != nil {
				return fmt.Errorf("exec in %q: %w", appName, err)
			}

			if output.IsJSON() {
				output.PrintJSON(result)
				if result.ExitCode != 0 {
					return exitWithCode(int(result.ExitCode), "command exited with code %d", result.ExitCode)
				}
				return nil
			}

			out := strings.TrimSpace(result.Output)
			if out != "" {
				fmt.Println(out)
			}

			if result.ExitCode != 0 {
				return exitWithCode(int(result.ExitCode), "command exited with code %d", result.ExitCode)
			}
			return nil
		},
	}

	return cmd
}
