package cli

import (
	"errors"
	"fmt"
	"io"
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
	var since string
	var until string
	var timestamps bool

	cmd := &cobra.Command{
		Use:   "logs [app]",
		Short: "Show container logs (or daemon logs if no app specified)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// No app specified → daemon logs
			if len(args) == 0 {
				if follow {
					return followDaemonLogs(c, tail)
				}
				logs, getErr := c.GetDaemonLogs(tail)
				if getErr != nil {
					return fmt.Errorf("get daemon logs: %w", getErr)
				}
				if output.IsJSON() {
					output.PrintJSON(map[string]string{"logs": logs})
					return nil
				}
				fmt.Print(logs)
				return nil
			}

			// App specified → container logs
			appName := args[0]
			if follow {
				return followLogs(c, appName, tail)
			}

			logs, err := c.GetAppLogs(appName, tail, since, until, timestamps)
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
	cmd.Flags().StringVar(&since, "since", "", "Show logs since timestamp (RFC3339) or relative (e.g. 1h)")
	cmd.Flags().StringVar(&until, "until", "", "Show logs until timestamp (RFC3339) or relative")
	cmd.Flags().BoolVarP(&timestamps, "timestamps", "t", false, "Show timestamps")

	return cmd
}

func followLogs(c *client.DaemonClient, appName string, lastTail int) error {
	reader, err := c.StreamAppLogs(appName, lastTail)
	if err != nil {
		return fmt.Errorf("stream logs: %w", err)
	}
	defer reader.Close()
	return streamToStdout(reader)
}

func followDaemonLogs(c *client.DaemonClient, lastTail int) error {
	reader, err := c.StreamDaemonLogs(lastTail)
	if err != nil {
		return fmt.Errorf("stream daemon logs: %w", err)
	}
	defer reader.Close()
	return streamToStdout(reader)
}

func streamToStdout(reader io.ReadCloser) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					errCh <- nil
				} else {
					errCh <- readErr
				}
				return
			}
		}
	}()

	select {
	case <-sigCh:
		return nil
	case readErr := <-errCh:
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "Log stream error: %v\n", readErr)
			return exitWithCode(ExitError, "log stream: %v", readErr)
		}
		return nil
	}
}
