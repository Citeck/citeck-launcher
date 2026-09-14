package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	return streamLiveStatus(c, reloadWaitOpts())
}

// reloadWaitOpts is the wait every "config changed, watch it land" caller
// performs: a short pause so the daemon picks the change up, then the live
// table until the apps settle.
func reloadWaitOpts() liveStatusOpts {
	ensureI18n()
	return liveStatusOpts{
		initialDelay: 1 * time.Second,
		successMsg:   output.Colorize(output.Green, t("reload.complete")),
	}
}

// terminalStartMessage words the end of a successful wait.
//
// "All apps started" would be a lie while apps sit HELD by a dependency the
// user stopped. A detached app is one the user named themselves; a held one is
// a second-order consequence of that, and hiding it behind the success line is
// how a stand that answers nothing looks like a successful start. The held line
// names the DETACHED apps to start, not the held ones — those release
// themselves.
func terminalStartMessage(held, total int, heldDeps []string, successMsg string) string {
	if held > 0 {
		return output.Colorize(output.Yellow, t("cli.appsHeldByStoppedDeps",
			"held", strconv.Itoa(held),
			"total", strconv.Itoa(total),
			"deps", strings.Join(heldDeps, ", ")))
	}
	if successMsg != "" {
		return successMsg
	}
	return t("cli.allAppsStarted")
}

// waitingDepNames lists the dependencies an app is held on, for a surface that
// has one app rather than a table.
func waitingDepNames(app api.AppDto) []string {
	names := make([]string, 0, len(app.WaitingFor))
	for _, dep := range app.WaitingFor {
		names = append(names, dep.App)
	}
	return names
}

// isAppTerminalFailed reports whether the given app status is a terminal
// failure state (i.e. the app won't reach RUNNING without external action).
func isAppTerminalFailed(status string) bool {
	switch status {
	case api.AppStatusStartFailed, api.AppStatusPullFailed, api.AppStatusFailed, api.AppStatusStoppingFailed:
		return true
	}
	return false
}

// isNsPrecommandSnapshot reports whether the namespace status indicates the
// daemon has not yet processed a just-issued lifecycle command. While NS sits
// in the stop domain (STOPPED/STOPPING), app statuses reflect the prior
// lifecycle — treating `stopped == total` as terminal would print "all apps
// started" immediately after `citeck start` on a freshly stopped namespace,
// before the daemon even pulls the first image. Callers poll again until the
// namespace transitions out of this domain.
func isNsPrecommandSnapshot(status string) bool {
	return status == api.NsStatusStopped || status == api.NsStatusStopping
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

		line := fmt.Sprintf("  %s  %s", appName, output.AppStatusCell(*app))
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
		case app.Status == api.AppStatusRunning:
			fmt.Printf("%s\n", output.Colorize(output.Green, //nolint:forbidigo // CLI success
				fmt.Sprintf("App %s: RUNNING", appName)))
			return nil
		case isAppTerminalFailed(app.Status):
			return exitWithCode(ExitError, "app %s: %s", appName, app.Status)
		case app.Held:
			// Held by a dependency the user detached: RestartApp on a
			// DEPS_WAITING app is an explicit no-op and nothing else will move
			// it, so polling for RUNNING here never ends. Name what to start.
			fmt.Printf("%s\n", output.Colorize(output.Yellow, //nolint:forbidigo // CLI result
				t("cli.appsHeldByStoppedDeps",
					"held", "1", "total", "1",
					"deps", strings.Join(waitingDepNames(*app), ", "))))
			return nil
		case app.Status == api.AppStatusStopped:
			// STOPPED is terminal only if the app was detached; for an active
			// start/restart, STOPPED briefly is normal (between stop → start).
			// Keep polling — the daemon will transition through STARTING again.
		}

		time.Sleep(2 * time.Second)
	}
}
