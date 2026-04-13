package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update workspace and bundle definitions",
		Long: `Pull latest workspace and bundle definitions from git, or import from a zip archive.

Without flags: pulls the latest commits from all configured git repos.
With --file: imports a workspace zip archive (for offline environments).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureI18n()

			if file != "" {
				return runUpdateFromFile(file)
			}
			return runUpdateFromGit()
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to workspace zip archive (offline import)")
	return cmd
}

// runUpdateFromGit pulls latest workspace and bundle repos.
func runUpdateFromGit() error {
	// Use a quiet (WARN-level) logger so per-call bundle-resolver bookkeeping
	// does not pollute CLI output. Pass it via the resolver instead of mutating
	// slog.Default() globally.
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	resolver := bundle.NewResolver(config.DataDir()).WithLogger(quiet)
	wsCfg := resolver.ResolveWorkspaceOnly()

	if len(wsCfg.BundleRepos) == 0 {
		output.PrintText("No bundle repos configured.")
		output.PrintText("Use --file to import a workspace archive.")
		return nil
	}

	updated := 0
	for _, repo := range wsCfg.BundleRepos {
		if repo.URL == "" {
			continue // local bundles, no git repo to pull
		}
		output.PrintText("Updating %s...", repo.ID)
		_, err := resolver.Resolve(bundle.Ref{Repo: repo.ID, Key: "LATEST"})
		switch {
		case err == nil:
			updated++
		case errors.Is(err, bundle.ErrNoBundles):
			// Pull succeeded but no bundle YAMLs were found — repo may use
			// a different layout or simply have nothing published yet.
			// This is not a user-facing error for `citeck update`; keep a
			// debug log for diagnostics and count the repo as "updated"
			// (the git pull itself worked).
			quiet.Debug("no bundles found after pull — repo layout mismatch or empty", "repo", repo.ID, "err", err)
			updated++
		default:
			output.PrintText("  Warning: %v", err)
		}
	}

	if updated == 0 {
		output.PrintText("Already up to date.")
	} else {
		output.PrintText("Updated %d repo(s).", updated)
	}
	return nil
}

// runUpdateFromFile imports a workspace zip archive.
func runUpdateFromFile(zipPath string) error {
	if _, err := os.Stat(zipPath); err != nil {
		return fmt.Errorf("file not found: %s", zipPath)
	}

	destDir := filepath.Join(config.DataDir(), "repo")

	// Check if destination already exists
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
		if !promptConfirm(fmt.Sprintf("Destination %s already exists with %d entries. Overwrite?", destDir, len(entries)), true) {
			output.PrintText("Canceled.")
			return nil
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

	output.PrintText("Imported %d files to %s", count, destDir)
	return nil
}
