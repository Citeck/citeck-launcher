package cli

import (
	"context"
	"errors"
	"fmt"
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
				output.PrintText(t("snapshot.list.empty"))
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
		Long:  "Stop the namespace (if running), export volumes, then start it back. Use --dir to specify the target directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// Resolve output directory: flag → interactive prompt → default
			dir := outputDir
			if dir == "" && !flagYes {
				dir = promptInput(t("snapshot.export.outputDir.label"), t("snapshot.export.outputDir.hint"), "")
			}

			// Check if namespace is running — offer to stop
			ns, nsErr := c.GetNamespace()
			wasRunning := nsErr == nil && ns != nil && ns.Status != "STOPPED"

			if wasRunning {
				if stopErr := stopForSnapshot(c); stopErr != nil {
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
					output.PrintText(t("snapshot.startingBack"))
					if _, startErr := c.StartNamespace(); startErr != nil {
						output.Errf("%s", t("snapshot.restartWarn", "error", startErr.Error()))
					}
				}
				return err
			}

			// Start namespace back if it was running
			if wasRunning {
				output.PrintText(t("snapshot.starting"))
				if _, startErr := c.StartNamespace(); startErr != nil {
					return fmt.Errorf("restart namespace after export: %w", startErr)
				}
				output.PrintText(t("snapshot.started"))
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&outputDir, "dir", "", "Write snapshot to this directory (absolute path)")
	return cmd
}

// stopForSnapshot prompts the user (unless --yes) and stops the namespace for a snapshot operation.
func stopForSnapshot(c *client.DaemonClient) error {
	if !promptConfirm(t("snapshot.stopConfirm"), true) {
		return errors.New(t("snapshot.stopCanceled"))
	}
	output.PrintText(t("snapshot.stopping"))
	if _, stopErr := c.StopNamespace(); stopErr != nil {
		return fmt.Errorf("stop namespace: %w", stopErr)
	}
	if waitErr := waitForStopped(c, 120*time.Second); waitErr != nil {
		return fmt.Errorf("waiting for stop: %w", waitErr)
	}
	output.PrintText(t("snapshot.stopped"))
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
	return errors.New(t("snapshot.stopTimeout", "status", lastStatus))
}

func newSnapshotImportCmd() *cobra.Command {
	var detach bool
	cmd := &cobra.Command{
		Use:   "import [snapshot-name]",
		Short: "Import a snapshot into namespace volumes",
		Long:  "Stop the namespace (if running), import volumes from snapshot, then start it back.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// Normalize snapshot name: append .zip if missing
			name := args[0]
			if !strings.HasSuffix(name, ".zip") {
				name += ".zip"
			}

			// Pre-flight validation: check snapshot exists before stopping namespace
			snapshots, listErr := c.ListSnapshots()
			if listErr != nil {
				return fmt.Errorf("list snapshots: %w", listErr)
			}
			found := false
			for _, s := range snapshots {
				if s.Name == name {
					found = true
					break
				}
			}
			if !found {
				return errors.New(t("snapshot.notFound", "name", name))
			}

			// Check if namespace is running — offer to stop
			ns, nsErr := c.GetNamespace()
			wasRunning := nsErr == nil && ns != nil && ns.Status != "STOPPED"

			if wasRunning {
				if stopErr := stopForSnapshot(c); stopErr != nil {
					return stopErr
				}
			}

			err = snapshotAndWait(c, "snapshot_complete", "snapshot_error", func() (*api.ActionResultDto, error) {
				return c.ImportSnapshot(name)
			})
			if err != nil {
				if wasRunning {
					output.PrintText(t("snapshot.startingBack"))
					if _, startErr := c.StartNamespace(); startErr != nil {
						output.Errf("%s", t("snapshot.restartWarn", "error", startErr.Error()))
					}
				}
				return err
			}

			if !wasRunning {
				return nil
			}

			output.PrintText(t("snapshot.starting"))
			if _, startErr := c.StartNamespace(); startErr != nil {
				return fmt.Errorf("restart namespace after import: %w", startErr)
			}

			if detach {
				output.PrintText(t("snapshot.import.detach"))
				return nil
			}

			// Wait for the namespace to reach RUNNING (or terminal) state.
			// StreamReloadStatus handles SIGINT internally and returns errInterrupted
			// on Ctrl+C — we treat that as "detach" (not an error).
			if waitErr := StreamReloadStatus(c); waitErr != nil {
				if errors.Is(waitErr, errInterrupted) {
					output.PrintText(t("snapshot.import.background"))
					return nil
				}
				return waitErr
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Don't wait for namespace to reach RUNNING after import")
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

	output.PrintText(t("snapshot.waitingCompletion"))
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
