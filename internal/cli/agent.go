package cli

import (
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent [operation-id]",
		Short: "Discover running Launcher API capabilities and lifecycle contracts (JSON)",
		Long:  "Read the running daemon's agent discovery API as JSON. With an operation id, return its detailed contract. This command never starts a daemon or changes containers.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to existing daemon: %w", err)
			}
			defer c.Close()
			var result any
			if len(args) == 0 {
				result, err = c.GetAgentCapabilities()
			} else {
				result, err = c.GetAgentOperation(args[0])
			}
			if err != nil {
				return fmt.Errorf("discover agent API: %w", err)
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		},
	}
}
