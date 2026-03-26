package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

func newUninstallCmd() *cobra.Command {
	var deleteData bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Citeck Launcher installation",
		Long:  "Removes the systemd service and optionally deletes all data (configs, volumes, snapshots).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(deleteData)
		},
	}

	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "Also delete all data (configs, volumes, snapshots)")

	return cmd
}

func runUninstall(deleteData bool) error {
	scanner := bufio.NewScanner(os.Stdin)

	// 1. Stop the daemon if running
	output.PrintText("Stopping daemon...")
	exec.Command("systemctl", "stop", "citeck").Run()

	// 2. Remove systemd service
	servicePath := "/etc/systemd/system/citeck.service"
	if _, err := os.Stat(servicePath); err == nil {
		if os.Getuid() != 0 {
			output.PrintText("Not running as root. To remove the service, run:")
			output.PrintText("  sudo systemctl stop citeck")
			output.PrintText("  sudo systemctl disable citeck")
			output.PrintText("  sudo rm %s", servicePath)
			output.PrintText("  sudo systemctl daemon-reload")
		} else {
			exec.Command("systemctl", "disable", "citeck").Run()
			os.Remove(servicePath)
			exec.Command("systemctl", "daemon-reload").Run()
			output.PrintText("Systemd service removed")
		}
	} else {
		output.PrintText("No systemd service found")
	}

	// 3. Optionally delete data
	if deleteData {
		if !flagYes {
			fmt.Printf("Delete ALL data in %s? [y/N]: ", config.HomeDir())
			scanner.Scan()
			if scanner.Text() != "y" && scanner.Text() != "yes" {
				output.PrintText("Aborted — data preserved")
				return nil
			}
		}

		dirs := []string{config.HomeDir()}
		for _, dir := range dirs {
			if _, err := os.Stat(dir); err == nil {
				if err := os.RemoveAll(dir); err != nil {
					output.PrintText("Failed to remove %s: %v", dir, err)
				} else {
					output.PrintText("Removed %s", dir)
				}
			}
		}
	}

	output.PrintText("Uninstall complete")
	return nil
}
