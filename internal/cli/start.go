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

// resolvePassword checks encryption state and returns the master password.
func resolvePassword(desktop bool) (string, error) {
	var store storage.Store
	var err error
	if desktop {
		store, err = storage.NewSQLiteStore(config.HomeDir())
	} else {
		store, err = storage.NewFileStore(config.ConfDir())
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
		pwdBytes, err := term.ReadPassword(syscall.Stdin)
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
	pwdBytes, err := term.ReadPassword(syscall.Stdin)
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
		confirmBytes, err := term.ReadPassword(syscall.Stdin)
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

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
			if c != nil {
				if showProgress && !lastTick.IsZero() {
					fmt.Println() //nolint:forbidigo // end progress line
				}
				return c, nil
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
}

// streamLiveStatus polls the daemon and shows an in-place table of app statuses.
func streamLiveStatus(c *client.DaemonClient, opts liveStatusOpts) error {
	ensureI18n()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if opts.initialDelay > 0 {
		time.Sleep(opts.initialDelay)
	}

	isTTY := output.IsTTY()
	firstPrint := true
	linesPrinted := 0
	lastRunning := -1

	for {
		select {
		case <-sigCh:
			fmt.Println() //nolint:forbidigo // clean newline on Ctrl+C
			return errInterrupted
		default:
		}

		ns, err := c.GetNamespace()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		table, running, failed, stopped, total := renderAppTable(ns.Apps)

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

		if total == 0 || opts.follow {
			time.Sleep(2 * time.Second)
			continue
		}

		// Guard against a stale pre-command snapshot: see isNsPrecommandSnapshot.
		if isNsPrecommandSnapshot(ns.Status) {
			time.Sleep(2 * time.Second)
			continue
		}

		// All non-detached apps reached RUNNING (detached apps count toward
		// stopped, which is terminal for our wait purposes) — draw the final
		// table without the summary and print the success message.
		if running+stopped == total {
			if isTTY && linesPrinted > 0 {
				output.ClearLines(linesPrinted)
				fmt.Println(table) //nolint:forbidigo // CLI table
			}
			ensureI18n()
			msg := opts.successMsg
			if msg == "" {
				msg = t("cli.allAppsStarted")
			}
			fmt.Printf("\n%s\n", msg) //nolint:forbidigo // CLI success
			return nil
		}

		// Some apps failed and we've reached a terminal state. Detached apps
		// (stopped) are terminal too — otherwise a failed + detached mix would
		// loop forever waiting for the STOPPED apps to "recover".
		if running+failed+stopped == total && !opts.waitAll {
			ensureI18n()
			fmt.Printf("\n%s\n", output.Colorize(output.Yellow,
				fmt.Sprintf("%d/%d apps started, %d failed", running, total, failed))) //nolint:forbidigo // CLI result
			return nil
		}

		// waitAll mode or not all terminal yet: keep polling.
		time.Sleep(2 * time.Second)
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
