package cli

import (
	"fmt"
	"os"
	"os/signal"
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
		return output.Colorize(output.Yellow, output.HeldSummary(held, total, heldDeps))
	}
	if successMsg != "" {
		return successMsg
	}
	return t("cli.allAppsStarted")
}

// singleAppHeldMessage words the end of a SINGLE-app wait that stopped because
// the app is held by a dependency the operator detached.
//
// The roots are THIS app's, not the namespace's: with two independent detached
// roots (`citeck stop postgres` and `citeck stop onlyoffice`) the
// namespace-wide answer sends somebody waiting on emodel off to start
// onlyoffice, which has nothing to do with emodel's hold. HeldRootsForApp still
// walks THROUGH intermediate held apps, which is what the namespace-wide call
// was here for — an app's own WaitingFor names a held neighbor the operator
// never stopped and cannot start (RestartApp is a no-op on DEPS_WAITING).
//
// It is a function and not three lines inside the poll loop for the reason the
// multi-app path already learned with terminalStartMessage: what the loop does
// is easy to test, WHICH NAME it prints is the part that was wrong, and a
// sentence built inline is a sentence no test can read.
func singleAppHeldMessage(apps []api.AppDto, appName string) string {
	return output.Colorize(output.Yellow, t("cli.appHeldByStoppedDeps",
		"app", appName,
		"deps", strings.Join(output.HeldRootsForApp(apps, appName), ", ")))
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
			fmt.Printf("%s\n", singleAppHeldMessage(ns.Apps, appName)) //nolint:forbidigo // CLI result
			return nil
		case app.Status == api.AppStatusStopped:
			// STOPPED is terminal only if the app was detached; for an active
			// start/restart, STOPPED briefly is normal (between stop → start).
			// Keep polling — the daemon will transition through STARTING again.
		}

		time.Sleep(2 * time.Second)
	}
}
