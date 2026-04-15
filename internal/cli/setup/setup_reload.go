package setup

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
)

// reloadAndWait triggers a namespace reload via daemon API and waits for all
// services to reach a terminal state. No-op if daemon is not running.
// Ctrl+C interrupts the wait — changes continue applying in the background.
func reloadAndWait() {
	c := client.TryNew(client.Options{})
	if c == nil {
		return // Daemon not running — changes will apply on next start.
	}
	defer c.Close()

	if !c.IsRunning() {
		return
	}

	fmt.Println() //nolint:forbidigo // CLI spacing
	output.PrintText(i18n.T("setup.reloading"))

	result, err := c.ReloadNamespace()
	if err != nil {
		slog.Warn("Reload failed after setup", "err", err)
		output.PrintText("  %s: %v", i18n.T("setup.reload_failed"), err)
		return
	}
	if !result.Success {
		output.PrintText("  %s: %s", i18n.T("setup.reload_failed"), result.Message)
		return
	}

	waitForServices(c)
}

// waitForServices polls namespace status until all apps reach a terminal state.
func waitForServices(c *client.DaemonClient) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	isTTY := output.IsTTY()
	firstPrint := true
	linesPrinted := 0
	lastRunning := -1

	// Brief pause to let the reconciler pick up changes.
	time.Sleep(1 * time.Second)

	for {
		select {
		case <-sigCh:
			fmt.Println() //nolint:forbidigo // clean newline
			output.PrintText(i18n.T("setup.reload_interrupted"))
			return
		default:
		}

		ns, pollErr := c.GetNamespace()
		if pollErr != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		r := output.FormatAppTable(ns.Apps)
		table, running, failed, stopped, total := r.Table, r.Running, r.Failed, r.Stopped, r.Total

		if isTTY {
			if !firstPrint && linesPrinted > 0 {
				output.ClearLines(linesPrinted)
			}
			firstPrint = false

			summary := fmt.Sprintf("  %d/%d running", running, total)
			if failed > 0 {
				summary += fmt.Sprintf(", %s", output.Colorize(output.Red, fmt.Sprintf("%d failed", failed)))
			}
			fmt.Println(table)   //nolint:forbidigo // CLI table
			fmt.Println()        //nolint:forbidigo // CLI spacing
			fmt.Println(summary) //nolint:forbidigo // CLI summary
			linesPrinted = strings.Count(table, "\n") + 3
		} else if running != lastRunning {
			fmt.Printf("  %d/%d running\n", running, total) //nolint:forbidigo // CLI progress
		}
		lastRunning = running

		// Exit when all apps reached a terminal state. STOPPED is terminal for
		// detached apps — the user intentionally took them offline, the
		// reconciler won't bring them back, so without counting them here the
		// loop would hang forever on any namespace with a detached service.
		if total > 0 && running+failed+stopped == total {
			if failed > 0 {
				fmt.Printf("\n%s\n", output.Colorize(output.Yellow,
					fmt.Sprintf("%d/%d running, %d failed", running, total, failed))) //nolint:forbidigo // CLI result
			} else {
				fmt.Printf("\n%s\n", output.Colorize(output.Green, i18n.T("setup.reload_complete"))) //nolint:forbidigo // success
			}
			return
		}

		time.Sleep(2 * time.Second)
	}
}
