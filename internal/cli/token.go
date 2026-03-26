package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage daemon API tokens",
	}

	cmd.AddCommand(newTokenGenerateCmd(), newTokenShowCmd())
	return cmd
}

func newTokenGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate a new daemon API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := generateToken()
			if err != nil {
				return fmt.Errorf("generate token: %w", err)
			}

			tokenPath := filepath.Join(config.ConfDir(), "daemon-token")
			if err := os.MkdirAll(filepath.Dir(tokenPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
				return fmt.Errorf("write token: %w", err)
			}

			output.PrintResult(map[string]string{"token": token, "path": tokenPath}, func() {
				output.PrintText("Token generated and saved to %s", tokenPath)
				output.PrintText("Token: %s", token)
			})

			// Try to notify running daemon to reload config
			if c := client.TryNew("", ""); c != nil {
				c.Close()
				output.PrintText("Note: restart daemon to apply the new token")
			}
			return nil
		},
	}
}

func newTokenShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current daemon API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenPath := filepath.Join(config.ConfDir(), "daemon-token")
			data, err := os.ReadFile(tokenPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no token found. Run 'citeck token generate' first")
				}
				return err
			}

			token := string(data)
			output.PrintResult(map[string]string{"token": token}, func() {
				output.PrintText(token)
			})
			return nil
		},
	}
}

func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
