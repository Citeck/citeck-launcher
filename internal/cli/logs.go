package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/niceteck/citeck-launcher/internal/client"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var tail int
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Show container logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			c, err := client.New(flagHost, flagToken)
			if err != nil {
				return err
			}
			defer c.Close()

			logs, err := c.GetAppLogs(appName, tail)
			if err != nil {
				return fmt.Errorf("get logs for %q: %w", appName, err)
			}

			if output.IsJSON() {
				output.PrintJSON(map[string]string{
					"app":  appName,
					"logs": logs,
				})
				return nil
			}

			fmt.Print(logs)

			if follow {
				return followLogs(c, appName, tail)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 100, "Number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")

	return cmd
}

func followLogs(c *client.DaemonClient, appName string, lastTail int) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return nil
		case <-ticker.C:
			logs, err := c.GetAppLogs(appName, lastTail)
			if err != nil {
				output.Errf("Error fetching logs: %v", err)
				continue
			}
			if logs != "" {
				fmt.Print(logs)
			}
		}
	}
}
