package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

// `citeck export` — the way an artifact leaves the box.
//
// Every container has an export directory (/citeck/export, a bind mount of
// rtfiles/export/<app>) for what it PRODUCES: a heap dump, a pg_dump, a thread
// dump. Those files sit on the daemon's host, which is not necessarily the
// host the operator is typing on.
//
// exportFileMode is 0600 on the receiving side on purpose: a heap dump holds
// every password, token and personal record the process had in memory, in the
// clear, and it is downloaded to whatever directory the operator happened to
// be standing in.
const exportFileMode os.FileMode = 0o600

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "List, download and remove files an app exported (heap dumps, pg_dumps, …)",
	}
	cmd.AddCommand(newExportLsCmd(), newExportGetCmd(), newExportRmCmd())
	return cmd
}

func newExportLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <app>",
		Short: "List an app's exported files, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			files, err := c.ListAppExports(args[0])
			if err != nil {
				return fmt.Errorf("list exports: %w", err)
			}
			if len(files) == 0 {
				output.PrintResult([]api.ExportFileDto{}, func() {
					output.PrintText("No exported files for %q.", args[0])
				})
				return nil
			}
			output.PrintResult(files, func() {
				rows := make([][]string, 0, len(files))
				for _, f := range files {
					rows = append(rows, []string{f.Name, formatExportSize(f.Size), f.Modified})
				}
				output.PrintText("%s", output.FormatTable([]string{"NAME", "SIZE", "MODIFIED"}, rows))
			})
			return nil
		},
	}
}

func newExportGetCmd() *cobra.Command {
	var out string
	var remove bool
	var force bool

	cmd := &cobra.Command{
		Use:   "get <app> <file>",
		Short: "Download an exported file to this machine",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			appName, fileName := args[0], args[1]
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			// The listing is what tells us the expected size (so a truncated
			// transfer is detected) and the daemon-side path (so a download on
			// the same host can skip the round trip entirely).
			files, err := c.ListAppExports(appName)
			if err != nil {
				return fmt.Errorf("list exports: %w", err)
			}
			meta, ok := findExportFile(files, fileName)
			if !ok {
				return fmt.Errorf("app %q has no exported file %q", appName, fileName)
			}

			dest := out
			if dest == "" {
				dest = filepath.Base(meta.Name)
			}
			if destErr := refuseExistingDest(dest, force); destErr != nil {
				return destErr
			}

			how, err := fetchExport(c, appName, meta, dest, remove)
			if err != nil {
				return err
			}
			output.PrintResult(map[string]any{
				"app": appName, "file": meta.Name, "path": dest,
				"size": meta.Size, "method": how, "removed": remove,
			}, func() {
				output.PrintText("Saved %s (%s) to %s", meta.Name, formatExportSize(meta.Size), dest)
				// Said every time, not once in the docs: this is the moment
				// the operator decides where the file ends up.
				output.PrintText("This file may contain passwords and personal data in the clear — it was written with mode 0600, keep it that way.")
			})
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Write to this path (default: the file's own name in the current directory)")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove the file from the export directory after a successful download")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the destination if it already exists")
	return cmd
}

func newExportRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <app> <file>",
		Short: "Remove an exported file from the export directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client.New(clientOpts())
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer c.Close()

			if err := c.DeleteAppExport(args[0], args[1]); err != nil {
				return fmt.Errorf("remove export: %w", err)
			}
			output.PrintResult(map[string]string{"app": args[0], "file": args[1], "status": "removed"}, func() {
				output.PrintText("Removed %s from %s's export directory.", args[1], args[0])
			})
			return nil
		},
	}
}

func findExportFile(files []api.ExportFileDto, name string) (api.ExportFileDto, bool) {
	for _, f := range files {
		if f.Name == name {
			return f, true
		}
	}
	return api.ExportFileDto{}, false
}

func refuseExistingDest(dest string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists (use --force to overwrite)", dest)
	}
	return nil
}

// fetchExport puts one export file at dest and reports how it got there
// ("moved", "copied" or "downloaded" — the JSON output carries it so a script
// can tell a local move from a transfer).
//
// Three routes, cheapest first. The first two only exist because the file can
// be as large as the heap that produced it: when the daemon runs on this same
// host the bytes are already here, and pushing gigabytes through the daemon to
// land them a directory away would be silly. Both local routes are attempted
// optimistically and fall through on ANY failure — a different uid, a
// different filesystem, a daemon on another machine that happens to report a
// path that also exists here — so correctness never depends on guessing right.
func fetchExport(c *client.DaemonClient, appName string, meta api.ExportFileDto, dest string, remove bool) (string, error) {
	if how, ok, err := tryLocalExport(c, appName, meta, dest, remove); ok {
		return how, err
	}

	body, err := c.StreamAppExport(appName, meta.Name)
	if err != nil {
		return "", fmt.Errorf("download export: %w", err)
	}
	defer func() { _ = body.Close() }()

	if err := writeExportStream(body, dest, meta.Size); err != nil {
		return "", err
	}
	if remove {
		return "downloaded", deleteExport(c, appName, meta.Name)
	}
	return "downloaded", nil
}

// tryLocalExport handles the case where the file is already on this host.
// ok=false means "not applicable, download it" — every local failure lands
// there, so correctness never depends on the shortcut being available.
func tryLocalExport(c *client.DaemonClient, appName string, meta api.ExportFileDto, dest string, remove bool) (how string, handled bool, err error) {
	if !localExportUsable(meta) {
		return "", false, nil
	}
	if remove {
		// A move is free and leaves nothing behind — which is exactly the
		// "dump, take it, clean up" flow.
		if renameErr := os.Rename(meta.HostPath, dest); renameErr == nil {
			// The container wrote this file with its own umask; the operator's
			// copy must not inherit that.
			if chmodErr := os.Chmod(dest, exportFileMode); chmodErr != nil {
				return "", true, fmt.Errorf("chmod %s: %w", dest, chmodErr)
			}
			return "moved", true, nil
		}
	}
	if copyErr := copyExportFile(meta.HostPath, dest, meta.Size); copyErr != nil {
		return "", false, nil
	}
	if !remove {
		return "copied", true, nil
	}
	// Deleted through the daemon rather than with os.Remove: one authority over
	// that directory, and it works when the local copy succeeded only because
	// the file happened to be world-readable.
	return "copied", true, deleteExport(c, appName, meta.Name)
}

func deleteExport(c *client.DaemonClient, appName, file string) error {
	if err := c.DeleteAppExport(appName, file); err != nil {
		return fmt.Errorf("remove %s from the export directory: %w", file, err)
	}
	return nil
}

// localExportUsable reports whether the daemon-reported path is a file THIS
// process can read, with the size the daemon reported. The size check is what
// makes the shortcut safe against a same-named file belonging to a different
// host's namespace.
func localExportUsable(meta api.ExportFileDto) bool {
	if meta.HostPath == "" {
		return false
	}
	info, err := os.Stat(meta.HostPath)
	if err != nil || info.IsDir() || info.Size() != meta.Size {
		return false
	}
	f, err := os.Open(meta.HostPath) //nolint:gosec // G304: path reported by the daemon this client is already trusting
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func copyExportFile(src, dest string, expect int64) error {
	f, err := os.Open(src) //nolint:gosec // G304: path reported by the daemon this client is already trusting
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = f.Close() }()
	return writeExportStream(f, dest, expect)
}

// writeExportStream streams src into dest and verifies the length.
//
// The size check is not ceremony: the download has no integrity envelope, the
// daemon sends the header before the body, and a connection dropped halfway
// through leaves a file that opens fine and is missing half the heap. Failing
// loudly beats handing someone a truncated dump to analyze.
func writeExportStream(src io.Reader, dest string, expect int64) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, exportFileMode) //nolint:gosec // G304: destination chosen by the operator
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	written, copyErr := io.Copy(f, src)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("write %s: %w", dest, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("close %s: %w", dest, closeErr)
	}
	if expect > 0 && written != expect {
		_ = os.Remove(dest)
		return fmt.Errorf("%s: expected %d bytes, got %d — the transfer was truncated", dest, expect, written)
	}
	// O_CREATE honors the umask, and an existing --force target keeps its old
	// mode, so set it explicitly.
	if err := os.Chmod(dest, exportFileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dest, err)
	}
	return nil
}

func formatExportSize(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
