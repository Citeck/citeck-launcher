package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage namespace snapshots (backup/restore)",
	}
	cmd.AddCommand(
		newSnapshotListCmd(),
		newSnapshotExportCmd(),
		newSnapshotImportCmd(),
	)
	return cmd
}

func newSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			snapshots, err := c.ListSnapshots()
			if err != nil {
				return fmt.Errorf("list snapshots: %w", err)
			}

			if len(snapshots) == 0 {
				output.PrintText("No snapshots found")
				return nil
			}

			output.PrintResult(snapshots, func() {
				for _, s := range snapshots {
					sizeMB := float64(s.Size) / (1024 * 1024)
					output.PrintText("  %-40s  %8.1f MB  %s", s.Name, sizeMB, s.CreatedAt)
				}
			})
			return nil
		},
	}
}

func newSnapshotExportCmd() *cobra.Command {
	var outputDir string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export namespace volumes to a snapshot",
		Long:  "Stop the namespace (if running), export volumes, then start it back. Use --output to specify the target directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// Resolve output directory: flag → interactive prompt → default
			dir := outputDir
			if dir == "" && !flagYes {
				fmt.Print("Output directory (empty for default snapshots dir): ") //nolint:forbidigo // CLI prompt
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				dir = strings.TrimSpace(scanner.Text())
			}

			// Check if namespace is running — offer to stop
			ns, nsErr := c.GetNamespace()
			wasRunning := nsErr == nil && ns != nil && ns.Status != "STOPPED"

			if wasRunning {
				if stopErr := stopForExport(c); stopErr != nil {
					return stopErr
				}
			}

			// Export (always wait for completion)
			err = snapshotAndWait(c, "snapshot_complete", "snapshot_error", func() (*api.ActionResultDto, error) {
				return c.ExportSnapshot(dir)
			})
			if err != nil {
				// Try to restart even on export failure
				if wasRunning {
					output.PrintText("Starting namespace back...")
					if _, startErr := c.StartNamespace(); startErr != nil {
						output.Errf("Warning: failed to restart namespace: %v", startErr)
					}
				}
				return err
			}

			// Start namespace back if it was running
			if wasRunning {
				output.PrintText("Starting namespace...")
				if _, startErr := c.StartNamespace(); startErr != nil {
					return fmt.Errorf("restart namespace after export: %w", startErr)
				}
				output.PrintText("Namespace started")
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&outputDir, "output", "", "Write snapshot to this directory (absolute path)")
	return cmd
}

// stopForExport prompts the user (unless --yes) and stops the namespace for snapshot export.
func stopForExport(c *client.DaemonClient) error {
	if !flagYes {
		fmt.Print("Namespace is running. Stop it for export? [Y/n]: ") //nolint:forbidigo // CLI prompt
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer == "n" || answer == "no" {
			return fmt.Errorf("export canceled — namespace must be stopped")
		}
	}
	output.PrintText("Stopping namespace...")
	if _, stopErr := c.StopNamespace(); stopErr != nil {
		return fmt.Errorf("stop namespace: %w", stopErr)
	}
	if waitErr := waitForStopped(c, 120*time.Second); waitErr != nil {
		return fmt.Errorf("waiting for stop: %w", waitErr)
	}
	output.PrintText("Namespace stopped")
	return nil
}

// waitForStopped polls namespace status until it's STOPPED or timeout.
func waitForStopped(c *client.DaemonClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		ns, nsErr := c.GetNamespace()
		if nsErr != nil {
			return fmt.Errorf("get namespace status: %w", nsErr)
		}
		lastStatus = ns.Status
		if lastStatus == "STOPPED" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for namespace to stop (stuck at %s)", lastStatus)
}

func newSnapshotImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import [snapshot-name]",
		Short: "Import a snapshot into namespace volumes (namespace must be stopped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			return snapshotAndWait(c, "snapshot_complete", "snapshot_error", func() (*api.ActionResultDto, error) {
				return c.ImportSnapshot(args[0])
			})
		},
	}
	return cmd
}

// snapshotAndWait subscribes to SSE events BEFORE sending the command (to avoid
// race where event fires before subscription), executes the action, then waits
// for a completion or error event.
func snapshotAndWait(c *client.DaemonClient, successType, errorType string, action func() (*api.ActionResultDto, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	events, err := c.StreamEvents(ctx)
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}

	result, err := action()
	if err != nil {
		return err
	}
	output.PrintText("%s", result.Message)

	output.PrintText("Waiting for completion...")
	for evt := range events {
		if evt.Type == successType {
			output.PrintText("%s", evt.After)
			return nil
		}
		if evt.Type == errorType {
			return fmt.Errorf("snapshot failed: %s", evt.After)
		}
	}
	return fmt.Errorf("event stream closed")
}
