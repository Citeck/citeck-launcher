package cli

import (
	"context"
	"fmt"
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
			c, err := client.New(flagHost, flagToken)
			if err != nil {
				return err
			}
			defer c.Close()

			snapshots, err := c.ListSnapshots()
			if err != nil {
				return err
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
	var wait bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export namespace volumes to a snapshot (namespace must be stopped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(flagHost, flagToken)
			if err != nil {
				return err
			}
			defer c.Close()

			return snapshotWithWait(c, wait, "snapshot_complete", "snapshot_error", func() (*api.ActionResultDto, error) {
				return c.ExportSnapshot()
			})
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Wait for export to complete")
	return cmd
}

func newSnapshotImportCmd() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "import [snapshot-name]",
		Short: "Import a snapshot into namespace volumes (namespace must be stopped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(flagHost, flagToken)
			if err != nil {
				return err
			}
			defer c.Close()

			return snapshotWithWait(c, wait, "snapshot_complete", "snapshot_error", func() (*api.ActionResultDto, error) {
				return c.ImportSnapshot(args[0])
			})
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Wait for import to complete")
	return cmd
}

// snapshotWithWait subscribes to SSE events BEFORE sending the command to avoid
// race conditions where the event fires before the subscription is ready.
func snapshotWithWait(c *client.DaemonClient, wait bool, successType, errorType string, action func() (*api.ActionResultDto, error)) error {
	var events <-chan api.EventDto
	if wait {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		var err error
		events, err = c.StreamEvents(ctx)
		if err != nil {
			return fmt.Errorf("connect to event stream: %w", err)
		}
	}

	result, err := action()
	if err != nil {
		return err
	}
	output.PrintResult(result, func() {
		output.PrintText("%s", result.Message)
	})

	if !wait {
		return nil
	}

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
