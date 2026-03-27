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
		Args:  cobra.MinimumNArgs(2),
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
				return err
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
