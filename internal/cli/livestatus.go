package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
)

// StreamReloadStatus waits for all services to reach terminal state after a reload/config change.
// Shows live table in TTY mode. Returns nil on success, errInterrupted on Ctrl+C.
// Exported so cli/setup can call it.
func StreamReloadStatus(c *client.DaemonClient) error {
	ensureI18n()
	err := streamLiveStatus(c, liveStatusOpts{
		initialDelay: 1 * time.Second,
		successMsg:   output.Colorize(output.Green, t("reload.complete")),
	})
	return err
}

// renderAppTable is a convenience wrapper around output.FormatAppTable
// that returns the counts used by streamLiveStatus. Stopped covers
// intentionally-detached apps (STOPPED only); STOPPING_FAILED is counted
// in Failed, matching isAppTerminalFailed below and the red colorization
// in output.ColorizeStatus. The wait-for-terminal check must include
// Stopped or the loop hangs on any namespace with a detached service.
func renderAppTable(apps []api.AppDto) (table string, running, failed, stopped, total int) {
	r := output.FormatAppTable(apps)
	return r.Table, r.Running, r.Failed, r.Stopped, r.Total
}

// isAppTerminalFailed reports whether the given app status is a terminal
// failure state (i.e. the app won't reach RUNNING without external action).
func isAppTerminalFailed(status string) bool {
	switch status {
	case "START_FAILED", "PULL_FAILED", "FAILED", "STOPPING_FAILED":
		return true
	}
	return false
}

// streamSingleAppStatus polls the daemon until the named app reaches RUNNING
// or a terminal failure state. Prints a one-line progress indicator in TTY
// mode (redrawn in place) or per-transition progress in non-TTY mode.
// Returns errInterrupted on Ctrl+C so callers can treat it as "continue in background".
func streamSingleAppStatus(c *client.DaemonClient, appName string) error {
	ensureI18n()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	isTTY := output.IsTTY()
	lastStatus := ""
	firstPrint := true
	linesPrinted := 0

	for {
		select {
		case <-sigCh:
			fmt.Println() //nolint:forbidigo // clean newline on Ctrl+C
			output.PrintText(t("cli.continueBackground"))
			return errInterrupted
		default:
		}

		ns, err := c.GetNamespace()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		var app *api.AppDto
		for i := range ns.Apps {
			if ns.Apps[i].Name == appName {
				app = &ns.Apps[i]
				break
			}
		}
		if app == nil {
			return fmt.Errorf("app %q not found", appName)
		}

		line := fmt.Sprintf("  %s  %s", appName, output.ColorizeStatus(app.Status))
		if isTTY {
			if !firstPrint && linesPrinted > 0 {
				output.ClearLines(linesPrinted)
			}
			firstPrint = false
			fmt.Println(line) //nolint:forbidigo // CLI live output
			linesPrinted = 1
		} else if app.Status != lastStatus {
			fmt.Println(line) //nolint:forbidigo // CLI progress
		}
		lastStatus = app.Status

		switch {
		case app.Status == "RUNNING":
			fmt.Printf("%s\n", output.Colorize(output.Green, //nolint:forbidigo // CLI success
				fmt.Sprintf("App %s: RUNNING", appName)))
			return nil
		case isAppTerminalFailed(app.Status):
			return exitWithCode(ExitError, "app %s: %s", appName, app.Status)
		case app.Status == "STOPPED":
			// STOPPED is terminal only if the app was detached; for an active
			// start/restart, STOPPED briefly is normal (between stop → start).
			// Keep polling — the daemon will transition through STARTING again.
		}

		time.Sleep(2 * time.Second)
	}
}
