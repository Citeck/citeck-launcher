package cli

import (
	"bufio"
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

const dropConfirmPhrase = "drop all data"

func runUninstall(deleteData bool) error {
	scanner := bufio.NewScanner(os.Stdin)

	// 1. Stop the daemon gracefully (stops containers + daemon)
	c := client.TryNew(clientOpts())
	if c != nil && c.IsRunning() {
		output.PrintText("Stopping platform...")
		_, _ = c.StopNamespace()
		_, _ = c.Shutdown()
		c.Close()
		// Wait for socket to disappear (up to 30s)
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

	// 3. Delete platform data
	homeDir := config.HomeDir()
	if deleteData {
		if err := os.RemoveAll(homeDir); err != nil {
			output.PrintText("  Failed to remove %s: %v", homeDir, err)
		} else {
			output.PrintText("  Removed %s", homeDir)
		}
		output.PrintText("\nUninstall complete")
		return nil
	}

	fmt.Println()                                                          //nolint:forbidigo // CLI output
	output.PrintText("  Platform data: %s", homeDir)
	output.PrintText("  To delete all data, type: %s", output.Colorize(output.Bold, dropConfirmPhrase))
	output.PrintText("  Press Enter to keep data.")
	for {
		fmt.Printf("\n  > ") //nolint:forbidigo // CLI prompt
		scanner.Scan()
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			output.PrintText("  Data preserved in %s", homeDir)
			break
		}
		if strings.EqualFold(input, dropConfirmPhrase) {
			if err := os.RemoveAll(homeDir); err != nil {
				output.PrintText("  Failed to remove %s: %v", homeDir, err)
			} else {
				output.PrintText("  Removed %s", homeDir)
			}
			break
		}
		output.PrintText("  Invalid input. Type \"%s\" or press Enter to skip.", dropConfirmPhrase)
	}

	output.PrintText("\nUninstall complete")
	return nil
}
