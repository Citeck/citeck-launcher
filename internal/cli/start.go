package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/storage"

	"golang.org/x/term"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

var errInterrupted = fmt.Errorf("interrupted")

func newStartCmd(version string) *cobra.Command {
	var foreground bool
	var desktop bool
	var noUI bool
	var offline bool
	var follow bool
	var detach bool
	var isDaemon bool

	cmd := &cobra.Command{
		Use:   "start [app]",
		Short: "Start the daemon and namespace (or a single app)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Hidden --_daemon mode: read password from stdin, run daemon blocking
			if isDaemon {
				return runDaemonMode(version, desktop, noUI, offline)
			}

			// --foreground means "be the daemon" — used by systemd ExecStart.
			// Don't peek at the socket: during `systemctl restart`, the previous
			// daemon may still be detached but alive, and we'd silently fall back
			// to client mode and exit, leaving the unit dead. Skip ahead to the
			// foreground block which calls daemon.Start() (binds the socket and
			// returns an error if it's truly in use).
			if !foreground {
				// If daemon is already running, send start command or stream status
				if c := client.TryNew(clientOpts()); c != nil {
					defer c.Close()
					return startOnRunningDaemon(c, args, detach, follow)
				}

				// Daemon not running
				if len(args) == 1 {
					ensureI18n()
					return errors.New(t("cli.daemonNotRunningStart"))
				}
			}

			// Server mode: require namespace.yml before starting
			if !desktop {
				if _, err := os.Stat(config.NamespaceConfigPath()); os.IsNotExist(err) {
					return fmt.Errorf("no namespace configured\n\nRun 'citeck install' to set up your namespace first")
				}
				// Check registry credentials for private image repos (interactive TTY only)
				if output.IsTTY() {
					if err := checkRegistryAuth(); err != nil {
						return err
					}
				}
			}

			// Foreground mode: run daemon directly (backward compat)
			if foreground {
				password, err := resolvePassword(desktop)
				if err != nil {
					return err
				}
				err = daemon.Start(daemon.StartOptions{
					Foreground:     true,
					Desktop:        desktop,
					NoUI:           noUI,
					Offline:        offline,
					Version:        version,
					MasterPassword: password,
				})
				if errors.Is(err, daemon.ErrShutdownRequested) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("daemon start: %w", err)
				}
				return nil
			}

			// Normal mode: resolve password, fork daemon, stream status
			password, err := resolvePassword(desktop)
			if err != nil {
				return err
			}

			// When systemd unit is installed and available, delegate to
			// `systemctl start citeck` instead of forking directly — the user
			// expects systemd-managed lifecycle (auto-restart, journald
			// logging, proper PID management). --detach forces manual fork.
			if !desktop && !detach && systemctlCanStartCiteck() {
				if sysErr := runSystemctl("start", "citeck"); sysErr != nil {
					return fmt.Errorf("systemctl start citeck: %w", sysErr)
				}
				c, waitErr := waitForDaemon(daemonStartupTimeout, output.IsTTY())
				if waitErr != nil {
					return fmt.Errorf("daemon failed to start: %w (check 'journalctl -u citeck')", waitErr)
				}
				defer c.Close()
				if streamErr := streamLiveStatus(c, liveStatusOpts{follow: follow}); streamErr != nil && !errors.Is(streamErr, errInterrupted) {
					return streamErr
				}
				return nil
			}

			if forkErr := forkDaemon(password, desktop, noUI, offline); forkErr != nil {
				return forkErr
			}

			// Wait for daemon to be ready
			c, err := waitForDaemon(daemonStartupTimeout, output.IsTTY())
			if err != nil {
				return fmt.Errorf("daemon failed to start: %w (check %s)", err, filepath.Join(config.LogDir(), "daemon.log"))
			}
			defer c.Close()

			if detach {
				ensureI18n()
				output.PrintText(t("cli.daemonStarted"))
				return nil
			}
			if err := streamLiveStatus(c, liveStatusOpts{follow: follow}); err != nil && !errors.Is(err, errInterrupted) {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground (don't fork)")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Start in background without waiting; bypasses systemd and forks daemon directly (like docker-compose up -d)")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Desktop mode (Wails)")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "Disable Web UI")
	cmd.Flags().BoolVar(&offline, "offline", false, "Offline mode: skip git operations, use only local data")
	cmd.Flags().BoolVar(&follow, "follow", false, "Don't exit after all apps are running")
	cmd.Flags().BoolVar(&isDaemon, "_daemon", false, "Internal: run as daemon process")
	_ = cmd.Flags().MarkHidden("_daemon")
	_ = cmd.Flags().MarkHidden("desktop")
	_ = cmd.Flags().MarkHidden("no-ui")

	return cmd
}

// stdinFd returns os.Stdin's descriptor as an int for golang.org/x/term.
// Isolating the single uintptr->int conversion here (instead of nolint-tagging
// every term.ReadPassword call) keeps gosec G115 quiet: a process's stdin fd is
// always a small non-negative value.
func stdinFd() int {
	return int(os.Stdin.Fd()) //nolint:gosec // G115: stdin fd is a small non-negative value
}

// resolvePassword checks encryption state and returns the master password.
func resolvePassword(desktop bool) (string, error) {
	var store storage.Store
	var err error
	if desktop {
		store, err = storage.NewSQLiteStore(config.HomeDir())
	} else {
		store, err = storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
	}
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	svc, err := storage.NewSecretService(store)
	if err != nil {
		return "", fmt.Errorf("check encryption: %w", err)
	}

	if !svc.IsEncrypted() {
		return "", nil // first run — no password needed
	}

	// Default password — try auto-unlock
	if svc.IsDefaultPassword() {
		if unlockErr := svc.Unlock(storage.DefaultMasterPassword); unlockErr == nil {
			return storage.DefaultMasterPassword, nil
		}
		// Default flag set but password doesn't match — fall through to prompt
	}

	// Prompt for password
	for range 3 {
		fmt.Print("Master password (empty to reset): ") //nolint:forbidigo // CLI prompt
		pwdBytes, err := term.ReadPassword(stdinFd())
		fmt.Println() //nolint:forbidigo // newline after password
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		password := string(pwdBytes)

		if password == "" {
			return handlePasswordReset(svc)
		}

		if unlockErr := svc.Unlock(password); unlockErr == nil {
			return password, nil
		}
		fmt.Println("Invalid password.") //nolint:forbidigo // CLI output
	}
	return "", fmt.Errorf("too many failed attempts")
}

// handlePasswordReset guides the user through resetting secrets.
func handlePasswordReset(svc *storage.SecretService) (string, error) {
	if !promptConfirm("All secrets will be regenerated. Continue?", false) {
		return "", fmt.Errorf("reset canceled")
	}

	if err := svc.ResetSecrets(); err != nil {
		return "", fmt.Errorf("reset secrets: %w", err)
	}

	fmt.Print("New master password (empty for default): ") //nolint:forbidigo // CLI prompt
	pwdBytes, err := term.ReadPassword(stdinFd())
	fmt.Println() //nolint:forbidigo // newline after password
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	newPassword := string(pwdBytes)
	isDefault := false

	if newPassword == "" {
		newPassword = storage.DefaultMasterPassword
		isDefault = true
	} else {
		// Confirm password
		fmt.Print("Confirm password: ") //nolint:forbidigo // CLI prompt
		confirmBytes, err := term.ReadPassword(stdinFd())
		fmt.Println() //nolint:forbidigo // newline after password
		if err != nil {
			return "", fmt.Errorf("read confirmation: %w", err)
		}
		if string(confirmBytes) != newPassword {
			return "", fmt.Errorf("passwords don't match")
		}
	}

	if err := svc.SetMasterPassword(newPassword, isDefault); err != nil {
		return "", fmt.Errorf("set password: %w", err)
	}
	fmt.Println("Password set. Secrets will be regenerated on start.") //nolint:forbidigo // CLI output
	return newPassword, nil
}

// systemctlCanStartCiteck reports whether a citeck.service systemd unit is
// installed AND `systemctl` is present on the system. When both conditions
// hold, `citeck start` delegates to `systemctl start citeck` so the user
// gets the systemd-managed lifecycle (journald logs, Restart=on-failure)
// they expect from a service they installed with a unit file.
//
// Detection is best-effort: if any probe (stat, LookPath, `systemctl
// --version`) fails we fall back to forkDaemon.
func systemctlCanStartCiteck() bool {
	if _, err := os.Stat(systemdUnitPath); err != nil {
		return false
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `systemctl --version` is a cheap liveness probe — exits non-zero on
	// systems where systemctl can't talk to PID 1 (e.g. non-systemd init).
	cmd := exec.Command("systemctl", "--version") //nolint:gosec // G204: constant args
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// forkDaemon starts the daemon as a detached child process.
func forkDaemon(password string, desktop, noUI, offline bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	logDir := config.LogDir()
	if mkErr := os.MkdirAll(logDir, 0o755); mkErr != nil { //nolint:gosec // log dir needs 0o755
		return fmt.Errorf("create log dir: %w", mkErr)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), //nolint:gosec // G302: log file needs 0o644; G304: path from trusted logDir
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	args := []string{"start", "--_daemon"}
	if desktop {
		args = append(args, "--desktop")
	}
	if noUI {
		args = append(args, "--no-ui")
	}
	if offline {
		args = append(args, "--offline")
	}
	cmd := exec.Command(exe, args...) //nolint:gosec // G204: exe is our own binary
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("fork daemon: %w", err)
	}
	_ = logFile.Close()

	fmt.Printf("Daemon started (PID %d)\n", cmd.Process.Pid) //nolint:forbidigo // CLI output
	return nil
}

// daemonStartupTimeout bounds how long the CLI waits for the daemon socket
// after `citeck install` / `citeck start`. The daemon can't open the socket
// until it finishes boot-time work: reading config, cloning the bundle repo
// (first time only — can be tens of seconds over a slow network), parsing
// workspace.yml, importing an auto-snapshot (if configured), and launching
// the runtime. 3 minutes comfortably covers first-install on a modest VPS
// without the user seeing a false-negative "daemon did not become ready"
// while the daemon is in fact booting.
const daemonStartupTimeout = 3 * time.Minute

// waitForDaemon polls the Unix socket until the daemon is ready. If
// showProgress is true, prints a dot roughly every 5 seconds so the user
// knows the wait is alive — meant for the install path where first-time
// bundle clone can take minutes on slow networks.
//
// "Ready" is NOT "the socket answers": the daemon binds it before the slow boot
// phase (git pull, Docker enumeration, namespace start) so that liveness can be
// probed immediately, and refuses every route but /health with
// DAEMON_STARTING meanwhile. Everything this function hands its client to —
// status streaming, the install wizard — would run straight into those 503s, so
// the wait ends only when the daemon's own health says it has finished booting.
func waitForDaemon(timeout time.Duration, showProgress bool) (*client.DaemonClient, error) {
	deadline := time.Now().Add(timeout)
	socketPath := config.SocketPath()
	lastTick := time.Now()

	for time.Now().Before(deadline) {
		// Try connecting to socket
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			// Socket is up — try creating a client
			c := client.TryNew(clientOpts())
			if c != nil && daemonFinishedBooting(c) {
				if showProgress && !lastTick.IsZero() {
					fmt.Println() //nolint:forbidigo // end progress line
				}
				return c, nil
			}
			if c != nil {
				c.Close()
			}
		}
		if showProgress && time.Since(lastTick) >= 5*time.Second {
			fmt.Print(".") //nolint:forbidigo // CLI progress dot
			lastTick = time.Now()
		}
		time.Sleep(500 * time.Millisecond)
	}
	if showProgress {
		fmt.Println() //nolint:forbidigo // end progress line
	}
	return nil, fmt.Errorf("timeout waiting for daemon socket at %s", socketPath)
}

// daemonFinishedBooting reports whether the daemon behind c has left its boot
// handler. A daemon too old to know about HealthStatusStarting never reports it,
// so this reads as "ready" there — which is what an upgrade rolling back to an
// older binary needs.
func daemonFinishedBooting(c *client.DaemonClient) bool {
	health, err := c.GetHealth()
	if err != nil || health == nil {
		return false
	}
	return health.Status != api.HealthStatusStarting
}

// runDaemonMode reads password from stdin and runs the daemon (blocking).
func runDaemonMode(version string, desktop, noUI, offline bool) error {
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	password := strings.TrimRight(line, "\n\r")

	err := daemon.Start(daemon.StartOptions{
		Foreground:     true,
		Desktop:        desktop,
		NoUI:           noUI,
		Offline:        offline,
		Version:        version,
		MasterPassword: password,
	})
	if errors.Is(err, daemon.ErrShutdownRequested) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("daemon start: %w", err)
	}
	return nil
}

// startOnRunningDaemon handles start commands when the daemon is already running.
func startOnRunningDaemon(c *client.DaemonClient, args []string, detach, follow bool) error {
	// App specified → start single app
	if len(args) == 1 {
		return startSingleApp(c, args[0], detach)
	}

	// No app → start namespace
	result, err := c.StartNamespace()
	if err != nil {
		return fmt.Errorf("start namespace: %w", err)
	}
	output.PrintText("%s", result.Message)
	if detach {
		return nil
	}
	return streamLiveStatus(c, liveStatusOpts{follow: follow})
}

// startSingleApp sends StartApp to the daemon and optionally streams live
// status for the named app until it reaches RUNNING or terminal failure.
func startSingleApp(c *client.DaemonClient, appName string, detach bool) error {
	result, err := c.StartApp(appName)
	if err != nil {
		return fmt.Errorf("start %q: %w", appName, err)
	}
	output.PrintResult(result, func() {
		output.PrintText(result.Message)
	})
	// The re-attach really happened; whether it was RECORDED is a separate
	// question (see state_write_report.go).
	warnIfStateNotSaved(c)
	// Fire-and-forget in --detach, JSON output, or non-TTY (scripts).
	if detach || output.IsJSON() || !output.IsTTY() {
		return nil
	}
	if waitErr := streamSingleAppStatus(c, appName); waitErr != nil {
		if errors.Is(waitErr, errInterrupted) {
			return nil
		}
		return waitErr
	}
	return nil
}

// liveStatusOpts configures streamLiveStatus behavior.
type liveStatusOpts struct {
	follow       bool          // keep streaming after all apps reach terminal state
	waitAll      bool          // wait until ALL apps are running (ignore intermediate failures, block until Ctrl+C or success)
	initialDelay time.Duration // pause before first poll (e.g. let reconciler pick up changes)
	successMsg   string        // custom message on all-running (default: cli.allAppsStarted)

	// The three bounds below are fields rather than constants so tests can
	// drive the loop in milliseconds; zero means "use the package default".
	pollInterval        time.Duration // between /namespace polls
	unproductiveGrace   time.Duration // continuous time the wait may spend unable to end by itself
	daemonFailureBudget time.Duration // continuous time the daemon may refuse to answer
}

const (
	// liveStatusPollInterval is the cadence of the /namespace polls.
	liveStatusPollInterval = 2 * time.Second

	// liveStatusUnproductiveGrace bounds a CONTINUOUS run of iterations that
	// cannot end the wait — a namespace sitting in the stop domain with no
	// update in flight (a reload of a stopped namespace enqueues a command
	// nothing drains), or a namespace with no apps at all. Two minutes is
	// generous enough that a namespace merely passing through STOPPING is
	// never cut short.
	liveStatusUnproductiveGrace = 2 * time.Minute

	// liveStatusDaemonFailureBudget bounds a CONTINUOUS run of failed
	// /namespace calls. It has to tolerate a daemon restart: `citeck upgrade`
	// swaps the binary and waits right afterwards.
	liveStatusDaemonFailureBudget = 2 * time.Minute

	// liveStatusErrorRetry is how long the loop waits after a failed poll.
	// Capped by the poll interval so a caller driving the loop faster does
	// not get a retry slower than its own cadence.
	liveStatusErrorRetry = 1 * time.Second
)

// streamLiveStatus polls the daemon and shows an in-place table of app statuses.
func streamLiveStatus(c *client.DaemonClient, opts liveStatusOpts) error {
	ensureI18n()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if opts.initialDelay > 0 {
		time.Sleep(opts.initialDelay)
	}

	poll := opts.pollInterval
	if poll <= 0 {
		poll = liveStatusPollInterval
	}
	errorRetry := min(liveStatusErrorRetry, poll)

	grace := opts.unproductiveGrace
	if grace <= 0 {
		grace = liveStatusUnproductiveGrace
	}
	budget := opts.daemonFailureBudget
	if budget <= 0 {
		budget = liveStatusDaemonFailureBudget
	}

	isTTY := output.IsTTY()
	firstPrint := true
	linesPrinted := 0
	lastRunning := -1
	// Start of the CURRENT continuous run of iterations that cannot end the
	// wait, and of the current continuous run of failed polls. Both are reset
	// by the first iteration that disproves them; zero means "no run open".
	var unproductiveSince, failingSince time.Time

	for {
		select {
		case <-sigCh:
			fmt.Println() //nolint:forbidigo // clean newline on Ctrl+C
			return errInterrupted
		default:
		}

		ns, err := c.GetNamespace()
		if err != nil {
			// A daemon that stops answering is worth retrying — `citeck
			// upgrade` swaps the binary and waits right afterwards, so the
			// socket legitimately goes away and comes back. Past the budget
			// it is no longer a hiccup: we know nothing about the namespace
			// any more, which is a failure and not a quiet success.
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			if time.Since(failingSince) >= budget {
				return fmt.Errorf("lost contact with the daemon (no answer for %s): %w", budget, err)
			}
			time.Sleep(errorRetry)
			continue
		}
		failingSince = time.Time{}

		appTable := output.FormatAppTable(ns.Apps)
		table, running, failed := appTable.Table, appTable.Running, appTable.Failed
		stopped, held, total := appTable.Stopped, appTable.Held, appTable.Total

		if isTTY {
			if !firstPrint && linesPrinted > 0 {
				output.ClearLines(linesPrinted)
			}
			firstPrint = false
			summary := buildStatusSummary(running, failed, total, opts.waitAll)
			fmt.Println(table)   //nolint:forbidigo // CLI table
			fmt.Println()        //nolint:forbidigo // CLI spacing
			fmt.Println(summary) //nolint:forbidigo // CLI summary
			linesPrinted = strings.Count(table, "\n") + 3
		} else if running != lastRunning {
			fmt.Printf("  %d/%d running\n", running, total) //nolint:forbidigo // CLI progress
		}
		lastRunning = running

		// A wait is PRODUCTIVE while it can still end on its own: --follow is
		// the user asking to keep watching; ns.Updating is the daemon saying it
		// is acting on the command (the reloadMu wait, the git pull, the bundle
		// resolve — minutes on a big bundle, throughout which the namespace
		// legitimately reads STOPPED); and a namespace out of the stop domain
		// with apps in it reaches the terminal checks below. Anything else is a
		// wait for something nobody is going to do — `citeck reload` on a
		// stopped namespace enqueues a command no loop drains — so it is
		// bounded. The command itself succeeded; only the watching ends, and
		// the table above has already been printed.
		if opts.follow || ns.Updating || (total > 0 && !isNsPrecommandSnapshot(ns.Status)) {
			unproductiveSince = time.Time{}
		} else {
			if unproductiveSince.IsZero() {
				unproductiveSince = time.Now()
			}
			if time.Since(unproductiveSince) >= grace {
				ensureI18n()
				// The status is rendered plain: output.Colorize appends a
				// reset, so a colorized status inside this line would end the
				// yellow halfway through the sentence.
				fmt.Printf("\n%s\n", output.Colorize(output.Yellow, //nolint:forbidigo // CLI result
					t("cli.waitEnded", "status", ns.Status)))
				return nil
			}
		}

		if total == 0 || opts.follow {
			time.Sleep(poll)
			continue
		}

		// Guard against a stale pre-command snapshot: see isNsPrecommandSnapshot.
		if isNsPrecommandSnapshot(ns.Status) {
			time.Sleep(poll)
			continue
		}

		// All non-detached apps reached RUNNING (detached apps count toward
		// stopped, which is terminal for our wait purposes) — draw the final
		// table without the summary and print the success message. An app HELD
		// by a detached dependency is terminal for the same reason the
		// dependency itself is: the user stopped it, and nothing here will
		// release the dependent until they start it again.
		if running+stopped+held == total {
			if isTTY && linesPrinted > 0 {
				output.ClearLines(linesPrinted)
				fmt.Println(table) //nolint:forbidigo // CLI table
			}
			ensureI18n()
			fmt.Printf("\n%s\n", //nolint:forbidigo // CLI result
				terminalStartMessage(held, total, appTable.HeldDeps, opts.successMsg))
			return nil
		}

		// Some apps failed and we've reached a terminal state. Detached apps
		// (stopped) and apps held by a detached dependency are terminal too —
		// otherwise a failed + detached mix would loop forever waiting for the
		// STOPPED apps to "recover".
		if running+failed+stopped+held == total && !opts.waitAll {
			ensureI18n()
			fmt.Printf("\n%s\n", output.Colorize(output.Yellow,
				fmt.Sprintf("%d/%d apps started, %d failed", running, total, failed))) //nolint:forbidigo // CLI result
			return nil
		}

		// waitAll mode or not all terminal yet: keep polling.
		time.Sleep(poll)
	}
}

// buildStatusSummary formats the live status summary line.
func buildStatusSummary(running, failed, total int, waitAll bool) string {
	summary := fmt.Sprintf("  %d/%d running", running, total)
	if failed > 0 {
		summary += ", " + output.Colorize(output.Red, fmt.Sprintf("%d failed", failed))
		if waitAll {
			summary += "  " + output.Colorize(output.Yellow, t("cli.waitingRetry"))
		}
	}
	return summary
}

// checkRegistryAuth verifies that credentials exist (and still work) for all
// private image registries used by the CURRENTLY configured bundle. Thin
// wrapper around checkRegistryAuthForBundle — see that function for details.
func checkRegistryAuth() error {
	nsCfg, err := namespace.LoadNamespaceConfig(config.NamespaceConfigPath())
	if err != nil {
		return nil // no namespace config — nothing to check yet
	}
	return checkRegistryAuthForBundle(nsCfg.BundleRef)
}
