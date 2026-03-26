package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
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

			// Follow mode: stream directly (the server sends tail+follow in one stream)
			if follow {
				return followLogs(c, appName, tail)
			}

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
			return nil
		},
	}

	cmd.Flags().IntVar(&tail, "tail", 100, "Number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")

	return cmd
}

func followLogs(c *client.DaemonClient, appName string, lastTail int) error {
	reader, err := c.StreamAppLogs(appName, lastTail)
	if err != nil {
		return fmt.Errorf("stream logs: %w", err)
	}
	defer reader.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case <-sigCh:
		return nil
	case <-done:
		return nil
	}
}
