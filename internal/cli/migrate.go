package cli

import (
	"fmt"

	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/h2migrate"
	"github.com/niceteck/citeck-launcher/internal/output"
	"github.com/niceteck/citeck-launcher/internal/storage"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate data from Kotlin desktop app (H2 → SQLite)",
		Long:  "Reads storage.db (H2 MVStore) from the desktop app directory and migrates workspaces, namespaces, and secrets to the new SQLite format.",
		RunE: func(cmd *cobra.Command, args []string) error {
			config.SetDesktopMode(true)
			homeDir := config.HomeDir()

			needed, err := h2migrate.NeedsMigration(homeDir)
			if err != nil {
				return fmt.Errorf("check migration: %w", err)
			}
			if !needed {
				output.PrintResult(map[string]any{
					"status": "skipped",
					"reason": "no migration needed (storage.db not found or launcher.db already exists)",
				}, func() {
					output.PrintText("No migration needed.")
				})
				return nil
			}

			store, err := storage.NewSQLiteStore(homeDir)
			if err != nil {
				return fmt.Errorf("create sqlite store: %w", err)
			}
			defer store.Close()

			result, err := h2migrate.Migrate(homeDir, store)
			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}

			output.PrintResult(result, func() {
				output.PrintText("Migration complete:")
				output.PrintText("  Workspaces: %d", result.Workspaces)
				output.PrintText("  Namespaces: %d", result.Namespaces)
				output.PrintText("  Secrets:    %d", result.Secrets)
				if result.Errors > 0 {
					output.PrintText("  Errors:     %d", result.Errors)
				}
			})
			return nil
		},
	}

	return cmd
}
