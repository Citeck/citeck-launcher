package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var watch bool
	var apps bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show namespace and app status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := client.TryNew(flagHost, flagToken)
			if c == nil {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText("Platform is not running")
				}
				return nil
			}
			defer c.Close()

			if !c.IsRunning() {
				if output.IsJSON() {
					output.PrintJSON(map[string]any{"running": false})
				} else {
					output.PrintText("Platform is not running")
				}
				return nil
			}

			ns, err := c.GetNamespace()
			if err != nil {
				return fmt.Errorf("get namespace: %w", err)
			}

			output.PrintResult(ns, func() {
				output.PrintText("%s  %s", output.Colorize(output.Bold, "Name:"), ns.Name)
				output.PrintText("%s  %s", output.Colorize(output.Bold, "Status:"), output.ColorizeStatus(ns.Status))
				if ns.BundleRef != "" {
					output.PrintText("%s  %s", output.Colorize(output.Bold, "Bundle:"), ns.BundleRef)
				}

				if (apps || len(ns.Apps) > 0) && len(ns.Apps) > 0 {
					fmt.Println()
					headers := []string{"APP", "STATUS", "IMAGE", "CPU", "MEMORY"}
					var rows [][]string
					for _, app := range ns.Apps {
						rows = append(rows, []string{
							app.Name,
							output.ColorizeStatus(app.Status),
							app.Image,
							app.CPU,
							app.Memory,
						})
					}
					output.PrintText(output.FormatTable(headers, rows))
				}
			})

			if watch {
				return watchEvents(c)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for changes (event stream)")
	cmd.Flags().BoolVarP(&apps, "apps", "a", false, "Show app details")

	return cmd
}

func watchEvents(c *client.DaemonClient) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel on interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}

	output.Errf("Watching events (Ctrl+C to stop)...")

	for evt := range events {
		ts := time.UnixMilli(evt.Timestamp).Format("15:04:05")
		if output.IsJSON() {
			output.PrintJSON(evt)
		} else {
			switch evt.Type {
			case "app_status":
				output.PrintText("[%s] %s  %s → %s", ts, evt.AppName, output.ColorizeStatus(evt.Before), output.ColorizeStatus(evt.After))
			case "namespace_status":
				output.PrintText("[%s] namespace  %s → %s", ts, output.ColorizeStatus(evt.Before), output.ColorizeStatus(evt.After))
			default:
				output.PrintText("[%s] %s  %s", ts, evt.Type, evt.After)
			}
		}
	}

	return nil
}
