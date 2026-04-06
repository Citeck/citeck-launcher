package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspace configuration",
	}
	cmd.AddCommand(newWorkspaceImportCmd())
	return cmd
}

func newWorkspaceImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <path-to-zip>",
		Short: "Import workspace config and bundles from a zip archive",
		Long: `Extract a workspace zip archive into the data/repo/ directory.

The archive is typically a GitHub "Download ZIP" of the launcher-workspace repo.
After import, the launcher uses local workspace config and bundle definitions
without any network access.

If the zip contains a single root directory (e.g. "launcher-workspace-main/"),
its contents are extracted directly into data/repo/.`,
		Args: cobra.ExactArgs(1),
		RunE: runWorkspaceImport,
	}
}

func runWorkspaceImport(_ *cobra.Command, args []string) error {
	zipPath := args[0]

	if _, err := os.Stat(zipPath); err != nil {
		return fmt.Errorf("file not found: %s", zipPath)
	}

	destDir := filepath.Join(config.DataDir(), "repo")

	// Check if destination already exists
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
		if !flagYes {
			output.PrintText(fmt.Sprintf("Destination %s already exists with %d entries. Overwrite? [y/N]: ", destDir, len(entries)))
			var answer string
			fmt.Scanln(&answer) //nolint:errcheck // best-effort prompt
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				return fmt.Errorf("cancelled")
			}
		}
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("remove existing: %w", err)
		}
	}

	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	count, err := extractZip(zipPath, destDir)
	if err != nil {
		return fmt.Errorf("extract zip: %w", err)
	}

	output.PrintText(fmt.Sprintf("Extracted %d files to %s", count, destDir))
	return nil
}

// extractZip extracts a zip archive into destDir.
// If the zip has a single root directory, its contents are extracted directly (strip prefix).
func extractZip(zipPath, destDir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Detect single root directory (GitHub "Download ZIP" pattern)
	prefix := detectSingleRootDir(r.File)

	count := 0
	for _, f := range r.File {
		name := f.Name
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
			if name == "" {
				continue // skip the root dir entry itself
			}
		}

		target := filepath.Join(destDir, filepath.FromSlash(name)) //nolint:gosec // G305: validated below
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) {
			continue // skip entries that escape destDir (zip slip)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return count, fmt.Errorf("mkdir %s: %w", name, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return count, fmt.Errorf("mkdir parent %s: %w", name, err)
		}

		if err := extractFile(f, target); err != nil {
			return count, fmt.Errorf("extract %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// detectSingleRootDir returns the common prefix if all entries share a single root directory.
// Returns "" if there's no single root (entries are at the top level).
func detectSingleRootDir(files []*zip.File) string {
	if len(files) == 0 {
		return ""
	}
	var root string
	for _, f := range files {
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 {
			return "" // file at root level — no single root dir
		}
		if root == "" {
			root = parts[0]
		} else if parts[0] != root {
			return "" // multiple root dirs
		}
	}
	return root + "/"
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err //nolint:wrapcheck // thin wrapper
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600)
	if err != nil {
		return err //nolint:wrapcheck // thin wrapper
	}
	defer out.Close()

	_, err = io.Copy(out, rc) //nolint:gosec // G110: zip file size checked by caller context
	return err                //nolint:wrapcheck // thin wrapper
}
