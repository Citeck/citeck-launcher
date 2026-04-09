package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newUninstallCmd() *cobra.Command {
	var deleteData bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Citeck Launcher installation",
		Long:  "Stops the platform, removes the systemd service, and optionally deletes all data.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(deleteData)
		},
	}

	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "Delete all data without prompting (for automation)")

	return cmd
}

// dropConfirmPhrase is intentionally English-only — a stable, language-independent
// confirmation phrase prevents accidental data deletion in unfamiliar languages.
const dropConfirmPhrase = "drop all data"

func runUninstall(deleteData bool) error {
	ensureI18n()

	// 1. Stop the daemon gracefully (stops containers + daemon)
	c := client.TryNew(clientOpts())
	if c != nil && c.IsRunning() {
		output.PrintText(t("uninstall.stopping"))
		_, _ = c.StopNamespace()
		_, _ = c.Shutdown()
		c.Close()
		socketPath := config.SocketPath()
		for range 30 {
			if _, err := os.Stat(socketPath); err != nil {
				break
			}
			time.Sleep(time.Second)
		}
	}

	// 2. Remove systemd service
	servicePath := "/etc/systemd/system/citeck.service"
	if _, err := os.Stat(servicePath); err == nil {
		if os.Getuid() != 0 {
			output.PrintText(t("uninstall.systemdNotRoot"))
			output.PrintText("  sudo systemctl stop citeck")
			output.PrintText("  sudo systemctl disable citeck")
			output.PrintText("  sudo rm %s", servicePath)
			output.PrintText("  sudo systemctl daemon-reload")
		} else {
			_ = exec.Command("systemctl", "disable", "citeck").Run()
			_ = os.Remove(servicePath)
			_ = exec.Command("systemctl", "daemon-reload").Run()
			output.PrintText(t("uninstall.systemdRemoved"))
		}
	} else {
		output.PrintText(t("uninstall.systemdNotFound"))
	}

	// 3. Delete platform data
	homeDir := config.HomeDir()
	if deleteData {
		if err := os.RemoveAll(homeDir); err != nil {
			output.PrintText(t("uninstall.dataRemoveFailed", "path", homeDir, "error", err.Error()))
		} else {
			output.PrintText(t("uninstall.dataRemoved", "path", homeDir))
		}
		output.PrintText("\n" + t("uninstall.complete"))
		return nil
	}

	fmt.Println() //nolint:forbidigo // CLI output
	output.PrintText(t("uninstall.dataPath", "path", homeDir))

	input := promptInput(
		t("uninstall.dataDropHint", "phrase", dropConfirmPhrase),
		t("uninstall.dataKeepHint"), "")
	if !strings.EqualFold(input, dropConfirmPhrase) {
		output.PrintText(t("uninstall.dataPreserved", "path", homeDir))
	} else {
		if rmErr := os.RemoveAll(homeDir); rmErr != nil {
			output.PrintText(t("uninstall.dataRemoveFailed", "path", homeDir, "error", rmErr.Error()))
		} else {
			output.PrintText(t("uninstall.dataRemoved", "path", homeDir))
		}
	}

	output.PrintText("\n" + t("uninstall.complete"))
	return nil
}
