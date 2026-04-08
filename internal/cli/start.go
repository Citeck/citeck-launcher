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

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"

	"golang.org/x/term"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

const defaultPassword = "citeck" //nolint:gosec // G101: well-known default, not a secret

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
				return fmt.Errorf("daemon is not running — start it first with 'citeck start'")
			}

			// Server mode: require namespace.yml before starting
			if !desktop {
				if _, err := os.Stat(config.NamespaceConfigPath()); os.IsNotExist(err) {
					return fmt.Errorf("no namespace configured\n\nRun 'citeck install' to set up your namespace first")
				}
				// Check registry credentials for private image repos (interactive TTY only)
				if isTTYOut() {
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

			if forkErr := forkDaemon(password, desktop, noUI, offline); forkErr != nil {
				return forkErr
			}

			// Wait for daemon to be ready
			c, err := waitForDaemon(30 * time.Second)
			if err != nil {
				return fmt.Errorf("daemon failed to start: %w (check %s)", err, filepath.Join(config.LogDir(), "daemon.log"))
			}
			defer c.Close()

			if detach {
				ensureI18n()
				output.PrintText(t("cli.daemonStarted"))
				return nil
			}
			return streamLiveStatus(c, follow)
		},
	}

	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground (don't fork)")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Start in background without waiting (like docker-compose up -d)")
	cmd.Flags().BoolVar(&desktop, "desktop", false, "Desktop mode")
	cmd.Flags().BoolVar(&noUI, "no-ui", false, "Disable Web UI")
	cmd.Flags().BoolVar(&offline, "offline", false, "Offline mode: skip git operations, use only local data")
	cmd.Flags().BoolVar(&follow, "follow", false, "Don't exit after all apps are running")
	cmd.Flags().BoolVar(&isDaemon, "_daemon", false, "Internal: run as daemon process")
	_ = cmd.Flags().MarkHidden("_daemon")

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
		if unlockErr := svc.Unlock(defaultPassword); unlockErr == nil {
			return defaultPassword, nil
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
	fmt.Print("All secrets will be regenerated. Continue? [y/N]: ") //nolint:forbidigo // CLI prompt
	// Read confirmation directly from fd (not bufio) to avoid buffering conflict with term.ReadPassword
	buf := make([]byte, 64)
	n, _ := os.Stdin.Read(buf)
	line := strings.TrimSpace(strings.ToLower(string(buf[:n])))
	if line != "y" && line != "yes" {
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
		newPassword = defaultPassword
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

// waitForDaemon polls the Unix socket until the daemon is ready.
func waitForDaemon(timeout time.Duration) (*client.DaemonClient, error) {
	deadline := time.Now().Add(timeout)
	socketPath := config.SocketPath()

	for time.Now().Before(deadline) {
		// Try connecting to socket
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			// Socket is up — try creating a client
			c := client.TryNew(clientOpts())
			if c != nil {
				return c, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
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
		appName := args[0]
		result, err := c.StartApp(appName)
		if err != nil {
			return fmt.Errorf("start %q: %w", appName, err)
		}
		output.PrintResult(result, func() {
			output.PrintText(result.Message)
		})
		return nil
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
	return streamLiveStatus(c, follow)
}

// streamLiveStatus polls the daemon and shows an in-place table of app statuses.
func streamLiveStatus(c *client.DaemonClient, follow bool) error {
	ensureI18n()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	isTTY := isTTYOut()
	firstPrint := true
	linesPrinted := 0
	lastRunning := -1

	for {
		select {
		case <-sigCh:
			fmt.Println() //nolint:forbidigo // clean newline on Ctrl+C
			return nil
		default:
		}

		ns, err := c.GetNamespace()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		table, running, _, total := renderAppTable(ns.Apps)

		if isTTY {
			// Clear previous output
			if !firstPrint && linesPrinted > 0 {
				clearLines(linesPrinted)
			}
			firstPrint = false

			// Print table + summary
			fmt.Println(table)            //nolint:forbidigo // CLI table
			fmt.Println()                 //nolint:forbidigo // CLI spacing
			fmt.Printf("  %d/%d running\n", running, total) //nolint:forbidigo // CLI summary
			linesPrinted = strings.Count(table, "\n") + 3 // table lines + blank + summary
		} else if running != lastRunning {
			// Non-TTY: print summary only when count changes
			fmt.Printf("  %d/%d running\n", running, total) //nolint:forbidigo // CLI progress
		}
		lastRunning = running

		if running == total && total > 0 && !follow {
			fmt.Printf("\n%s\n", t("cli.allAppsStarted", "count", fmt.Sprintf("%d", total))) //nolint:forbidigo // CLI success
			return nil
		}

		time.Sleep(2 * time.Second)
	}
}

// checkRegistryAuth verifies that credentials exist for all private image registries.
// If missing, prompts the user to enter them (with docker login validation).
func checkRegistryAuth() error {
	resolver := bundle.NewResolver(config.DataDir())
	resolver.SetOffline(true) // don't trigger git pull on every start
	wsCfg := resolver.ResolveWorkspaceOnly()

	// Scope auth check to repos used by the configured bundle
	nsCfg, nsErr := namespace.LoadNamespaceConfig(config.NamespaceConfigPath())
	var usedIDs map[string]bool
	if nsErr == nil {
		usedIDs = bundleImageRepoIDs(nsCfg.BundleRef, wsCfg)
	}
	authRepos := findAuthRepos(wsCfg, usedIDs)
	if len(authRepos) == 0 {
		return nil
	}

	svc, svcErr := openSecretService()
	if svcErr != nil {
		return nil // can't check — daemon will handle it
	}

	// Find repos missing credentials
	var missing []bundle.ImageRepo
	for _, repo := range authRepos {
		sec, _ := svc.GetSecret("registry-" + repo.ID)
		if sec == nil || sec.Value == "" {
			missing = append(missing, repo)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	ensureI18n()

	scanner := bufio.NewScanner(os.Stdin)
	for _, repo := range missing {
		host := registryHost(repo.URL)
		output.PrintText("%s: %s", t("install.registry.host"), host)
		for {
			username := promptText(scanner, t("install.registry.username"), "", "")
			if username == "" {
				return fmt.Errorf("registry credentials required for %s\n\nConfigure via 'citeck install' or provide credentials", host)
			}
			password := promptText(scanner, t("install.registry.password"), "", "")
			if password == "" {
				continue
			}

			output.PrintText("  %s", t("install.registry.checking"))
			if loginErr := dockerRegistryLogin(host, username, password); loginErr != nil {
				output.Errf("  %s: %v", t("install.registry.failed"), loginErr)
				continue // retry
			}
			output.PrintText("  %s", t("install.registry.success"))

			if saveErr := saveRegistrySecret(svc, repo, username, password); saveErr != nil {
				output.Errf("  %s: %v", t("install.registry.saveFailed"), saveErr)
			}
			break
		}
	}
	return nil
}
