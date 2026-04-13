package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/output"
)

// Installer lifecycle: extends `citeck install` to handle its own binary
// bootstrap (fresh install, in-place upgrade, rollback). All the complex
// download/kill/swap logic that used to live in install.sh lives here now.
//
// The script only needs to fetch the latest release and exec `<binary>
// install` — this file takes over from there.

const (
	installTarget       = "/usr/local/bin/citeck"
	installBackupSuffix = ".bak"
	systemdUnitPath     = "/etc/systemd/system/citeck.service"
	daemonStopTimeout   = 30 * time.Second
	versionProbeTimeout = 5 * time.Second

	// systemdRuntimeDropInDir is a tmpfs-backed override directory (cleared
	// on reboot) we use to install a temporary Restart=no drop-in during
	// the SIGKILL upgrade window. We can't use `systemctl mask` because
	// mask creates a symlink at /etc/systemd/system/citeck.service → /dev/null
	// and refuses when the real unit file already exists there.
	systemdRuntimeDropInDir  = "/run/systemd/system/citeck.service.d"
	systemdRuntimeDropInFile = systemdRuntimeDropInDir + "/no-restart.conf"
	systemdRuntimeDropInBody = "[Service]\nRestart=no\n"

	// installerCacheEnv is the env var install.sh sets to tell the binary
	// where it was downloaded to. On successful install the binary removes
	// that file so subsequent runs re-download fresh; on failure the file
	// stays put so the next run reuses it instead of re-fetching.
	installerCacheEnv = "CITECK_INSTALLER_CACHE"
)

// installerState is process-local transient state shared between the stop
// and start phases of the lifecycle — in particular, whether we installed
// a runtime Restart=no drop-in so the stop-phase SIGKILL wouldn't trigger
// Restart=on-failure respawn of the old binary. start phase removes the
// drop-in before starting.
var installerState struct {
	systemdDropInInstalled bool
}

// cleanupInstallerCacheOnSuccess removes the downloaded installer binary
// referenced by CITECK_INSTALLER_CACHE (if set). Called from runInstall's
// deferred finalizer only on a clean exit — if the install errors out, the
// cache file is intentionally left in place so a re-run of install.sh can
// reuse it instead of re-downloading from GitHub.
//
// No-op when the env var is unset (binary was invoked directly, not from
// the install.sh bootstrap) or the file is already gone.
func cleanupInstallerCacheOnSuccess() {
	cachePath := os.Getenv(installerCacheEnv)
	if cachePath == "" {
		return
	}
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) { //nolint:gosec // G304/G703: cachePath is from our own install.sh bootstrap, propagated via CITECK_INSTALLER_CACHE env var
		output.PrintText("  warn: failed to clean up installer cache %s: %v", cachePath, err)
	}
}

// handleInstallerLifecycle detects whether this process is running as the
// installer (self is outside /usr/local/bin/citeck) and dispatches to the
// appropriate lifecycle operation: fresh install copy, same-version re-exec,
// or upgrade flow. Returns handled=true when the lifecycle took over and
// the caller should return err immediately; handled=false means "not the
// installer scenario, continue with the regular wizard".
func handleInstallerLifecycle(info BuildInfo) (handled bool, err error) {
	selfPath, selfErr := os.Executable()
	if selfErr != nil {
		return false, nil // couldn't resolve, fall through to wizard
	}
	// Running from /usr/local/bin/citeck itself — not the installer scenario.
	if installedAtTarget(selfPath) {
		return false, nil
	}

	// Initialize i18n before any user-facing output in the lifecycle paths
	// below (without this, prompt Descriptions like `t("hint.confirm")` render
	// as the raw key string in the confirm dialog).
	ensureI18n()

	// Target doesn't exist: fresh install.
	if _, statErr := os.Stat(installTarget); os.IsNotExist(statErr) {
		return true, lifecycleFreshInstall(selfPath)
	}

	// Target exists: compare binary hashes to detect any change (including
	// rebuilds of the same version). Fall back to version comparison if
	// hashing fails.
	installedVer := readBinaryVersion(installTarget)
	if installedVer == "" {
		return true, fmt.Errorf("could not determine installed version at %s — remove it and try again", installTarget)
	}

	// Same binary (hash match) — hand off to the installed binary.
	if hashesMatch(selfPath, installTarget) {
		output.PrintText(t("install.lifecycle.alreadyInstalled", "ver", installedVer, "path", installTarget))
		return true, reExecAtTarget(installTarget)
	}

	// Different binary: upgrade.
	return true, lifecycleUpgrade(selfPath, installedVer, info.Version)
}

// installedAtTarget reports whether selfPath points to the same file as the
// install target (handling symlinks + path normalization via os.SameFile).
func installedAtTarget(selfPath string) bool {
	selfInfo, err := os.Stat(selfPath)
	if err != nil {
		return false
	}
	targetInfo, err := os.Stat(installTarget)
	if err != nil {
		return false
	}
	return os.SameFile(selfInfo, targetInfo)
}

// lifecycleFreshInstall copies self to /usr/local/bin/citeck and re-execs
// from that path so the wizard's forkDaemon uses the installed location.
func lifecycleFreshInstall(selfPath string) error {
	output.PrintText(t("install.lifecycle.freshInstall.installing", "path", installTarget))
	if err := copyBinaryAtomic(selfPath, installTarget); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	output.PrintText(t("install.lifecycle.freshInstall.done"))
	return reExecAtTarget(installTarget)
}

// lifecycleUpgrade backs up the current binary, stops the old daemon while
// preserving platform containers, atomically swaps the binary on disk, and
// starts the new daemon. The Docker containers stay running the entire time
// — Docker (not the launcher) owns them, and the new daemon adopts them via
// doStart's hash-matching path.
func lifecycleUpgrade(selfPath, installedVer, newVer string) error {
	output.PrintText("")
	output.PrintText("  " + t("install.lifecycle.upgrade.installedVersion", "ver", installedVer))
	output.PrintText("  " + t("install.lifecycle.upgrade.availableVersion", "ver", newVer))
	output.PrintText("")
	if !promptConfirm(t("install.lifecycle.upgrade.prompt", "ver", newVer), true) {
		output.PrintText(t("install.lifecycle.upgrade.canceled"))
		return nil
	}

	// Backup current binary first — if the swap fails partway through, the
	// user can recover with `citeck install --rollback`.
	bakPath := installTarget + installBackupSuffix
	output.PrintText("Backing up current binary to %s", bakPath)
	if err := copyBinaryAtomic(installTarget, bakPath); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	output.PrintText("Stopping old daemon (platform containers stay running)...")
	if err := stopDaemonPreservePlatform(installedVer); err != nil {
		return fmt.Errorf("stop old daemon: %w", err)
	}

	output.PrintText("Installing new binary...")
	if err := copyBinaryAtomic(selfPath, installTarget); err != nil {
		return fmt.Errorf("copy new binary: %w", err)
	}

	output.PrintText("Starting new daemon...")
	if err := startDaemonAfterSwap(); err != nil {
		return fmt.Errorf("start new daemon: %w", err)
	}

	output.PrintText("Upgrade complete: %s -> %s", installedVer, newVer)
	return nil
}

// runRollback restores the previous binary from .bak and restarts the daemon.
// Used via `citeck install --rollback`.
func runRollback() error {
	bakPath := installTarget + installBackupSuffix
	if _, err := os.Stat(bakPath); err != nil {
		return fmt.Errorf("no backup at %s — nothing to rollback", bakPath)
	}

	currentVer := readBinaryVersion(installTarget)
	backupVer := readBinaryVersion(bakPath)
	output.PrintText("")
	output.PrintText("  " + t("install.lifecycle.rollback.currentVersion", "ver", currentVer))
	output.PrintText("  " + t("install.lifecycle.rollback.backupVersion", "ver", backupVer))
	output.PrintText("")
	if !promptConfirm(t("install.lifecycle.rollback.prompt"), true) {
		output.PrintText(t("install.lifecycle.rollback.canceled"))
		return nil
	}

	output.PrintText(t("install.lifecycle.rollback.stopping"))
	if err := stopDaemonPreservePlatform(currentVer); err != nil {
		return fmt.Errorf("stop daemon: %w", err)
	}

	output.PrintText(t("install.lifecycle.rollback.restoring"))
	if err := copyBinaryAtomic(bakPath, installTarget); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	_ = os.Remove(bakPath)

	output.PrintText(t("install.lifecycle.rollback.starting"))
	if err := startDaemonAfterSwap(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	output.PrintText(t("install.lifecycle.rollback.done", "from", currentVer, "to", backupVer))
	return nil
}

// hashesMatch returns true if the two files have identical SHA256 hashes.
// Returns false on any error (missing file, read error) so the caller
// falls through to the upgrade path.
func hashesMatch(a, b string) bool {
	hashA, errA := fileSHA256(a)
	hashB, errB := fileSHA256(b)
	if errA != nil || errB != nil {
		return false
	}
	return hashA == hashB
}

// fileSHA256 returns the hex-encoded SHA256 hash of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is from os.Executable() or constant install target
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// copyBinaryAtomic writes src to dst via fsutil.AtomicWriteFile which uses a
// temp-file + fsync + rename pattern. Rename is safe even if dst is currently
// being executed: Linux replaces the directory entry atomically, the old
// inode stays alive as long as a running process holds it open.
func copyBinaryAtomic(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // G304: src is from os.Executable() or constant install path
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := fsutil.AtomicWriteFile(dst, data, 0o755); err != nil { //nolint:gosec // G302: binary needs 0o755 to be executable
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// reExecAtTarget replaces the current process image with an invocation of
// the target binary, passing the same arguments. syscall.Exec is the POSIX
// execve syscall — the current PID is preserved, only the binary changes.
// Used after a fresh install so wizard/forkDaemon pick up the installed path.
func reExecAtTarget(target string) error {
	argv := append([]string{target}, os.Args[1:]...)
	if err := syscall.Exec(target, argv, os.Environ()); err != nil { //nolint:gosec // G204: target is a compile-time constant
		return fmt.Errorf("exec %s: %w", target, err)
	}
	return nil
}

// readBinaryVersion runs `<path> version --short` to get the installed
// version. Falls back to parsing the first "Citeck CLI X.Y.Z" line from the
// regular `version` output for v2.0.0 which lacks the --short flag.
func readBinaryVersion(path string) string {
	if v := runVersionShort(path); v != "" {
		return v
	}
	return runVersionFallback(path)
}

func runVersionShort(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--short").Output() //nolint:gosec // G204: path comes from trusted constant or os.Executable()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
}

func runVersionFallback(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output() //nolint:gosec // G204: path comes from trusted constant
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Citeck CLI") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			return strings.TrimPrefix(parts[len(parts)-1], "v")
		}
	}
	return ""
}

// stopDaemonPreservePlatform stops the running daemon without touching the
// platform containers. Uses the clean HTTP detach (`ShutdownLeaveRunning`)
// for v2.1.0+ and falls back to SIGKILL for v2.0.0 — Docker owns the
// containers, so a hard kill of the orchestrator process leaves them alive.
func stopDaemonPreservePlatform(installedVer string) error {
	c := client.TryNew(clientOpts())
	if c == nil {
		return nil // no daemon running
	}
	defer c.Close()
	if !c.IsRunning() {
		return nil
	}

	if versionAtLeast(installedVer, "2.1.0") {
		if _, err := c.ShutdownLeaveRunning(); err == nil {
			return waitForDaemonStop()
		}
		output.PrintText("  warn: clean detach failed, falling back to SIGKILL")
	}
	return sigkillPreservePlatform(c)
}

// sigkillPreservePlatform fetches the daemon's PID and socket path via the
// status API, installs a runtime Restart=no drop-in for the citeck.service
// unit (if present) so Restart=on-failure doesn't respawn the old binary
// during the swap window, sends SIGKILL, waits for the process to actually
// exit, and cleans up the orphaned Unix socket file.
func sigkillPreservePlatform(c *client.DaemonClient) error {
	status, err := c.GetStatus()
	if err != nil {
		return fmt.Errorf("get daemon status: %w", err)
	}
	if status.PID == 0 {
		return errors.New("daemon status returned zero PID")
	}
	pid := int(status.PID)

	// Install Restart=no drop-in BEFORE the kill so auto-restart doesn't
	// fire during the swap window.
	if _, statErr := os.Stat(systemdUnitPath); statErr == nil {
		if runErr := installSystemdNoRestartDropIn(); runErr != nil {
			output.PrintText("  warn: failed to install Restart=no drop-in: %v", runErr)
		} else {
			installerState.systemdDropInInstalled = true
		}
	}

	proc, findErr := os.FindProcess(pid)
	if findErr != nil {
		return fmt.Errorf("find daemon process %d: %w", pid, findErr)
	}
	if killErr := proc.Signal(syscall.SIGKILL); killErr != nil && !strings.Contains(killErr.Error(), "process already finished") {
		return fmt.Errorf("kill daemon process %d: %w", pid, killErr)
	}

	// Poll signal-0 (existence check) until the kernel reports process gone.
	deadline := time.Now().Add(daemonStopTimeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return fmt.Errorf("daemon PID %d still alive after SIGKILL + %s wait", pid, daemonStopTimeout)
	}

	// Clean up orphaned Unix socket so the new daemon can bind cleanly.
	// The new daemon's own DetectTransport would also remove it after a dial
	// check, but doing it here removes the race between kill and startup.
	if status.SocketPath != "" {
		_ = os.Remove(status.SocketPath)
	}
	return nil
}

// waitForDaemonStop polls the daemon client until it becomes unreachable
// (after a clean detach) or the timeout expires.
func waitForDaemonStop() error {
	deadline := time.Now().Add(daemonStopTimeout)
	for time.Now().Before(deadline) {
		c := client.TryNew(clientOpts())
		if c == nil {
			return nil
		}
		running := c.IsRunning()
		c.Close()
		if !running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not stop within %s", daemonStopTimeout)
}

// startDaemonAfterSwap brings the new binary online. Uses systemctl when a
// citeck.service unit exists (the install wizard creates one with
// Restart=on-failure); otherwise forks a detached daemon directly.
func startDaemonAfterSwap() error {
	if _, err := os.Stat(systemdUnitPath); err == nil {
		if installerState.systemdDropInInstalled {
			if removeErr := removeSystemdNoRestartDropIn(); removeErr != nil {
				output.PrintText("  warn: failed to remove Restart=no drop-in: %v", removeErr)
			}
			installerState.systemdDropInInstalled = false
		}
		return runSystemctl("start", "citeck")
	}
	// No systemd: fork a detached daemon via the installed binary. Inherit
	// stdin so any master-password prompt reaches the user's terminal.
	cmd := exec.Command(installTarget, "start", "--detach") //nolint:gosec // G204: installTarget is a compile-time constant
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fork daemon: %w", err)
	}
	return nil
}

// installSystemdNoRestartDropIn writes a runtime drop-in that overrides
// Restart=no for the citeck.service unit, preventing systemd from respawning
// the old binary after SIGKILL during the swap window. The drop-in lives
// under /run/systemd (tmpfs) so it's automatically cleared on reboot if
// we crash before removing it ourselves.
//
// We can't use `systemctl mask` because mask creates a symlink at
// /etc/systemd/system/<unit> → /dev/null and refuses when a real unit file
// exists there (which is exactly our case — the install wizard writes one).
//
// install.sh elevates the installer via `sudo -E` before executing the
// binary, so in the normal upgrade flow we're already root here and direct
// Go file ops work. The non-root path (manual invocation via `citeck install`
// on a user that can write /run/systemd) is rare; it falls through to the
// same code since /run/systemd is typically world-writable for root only.
func installSystemdNoRestartDropIn() error {
	if os.Geteuid() != 0 {
		return errors.New("need root to write " + systemdRuntimeDropInFile)
	}
	if err := os.MkdirAll(systemdRuntimeDropInDir, 0o755); err != nil { //nolint:gosec // G301: systemd drop-in dirs conventionally use 0o755 so systemd (running as root) can read them
		return fmt.Errorf("mkdir %s: %w", systemdRuntimeDropInDir, err)
	}
	if err := os.WriteFile(systemdRuntimeDropInFile, []byte(systemdRuntimeDropInBody), 0o644); err != nil { //nolint:gosec // G306: systemd drop-ins conventionally use 0o644
		return fmt.Errorf("write %s: %w", systemdRuntimeDropInFile, err)
	}
	return runSystemctl("daemon-reload")
}

// removeSystemdNoRestartDropIn reverses installSystemdNoRestartDropIn so the
// unit reverts to its configured Restart=on-failure behavior.
func removeSystemdNoRestartDropIn() error {
	if os.Geteuid() != 0 {
		return errors.New("need root to remove " + systemdRuntimeDropInFile)
	}
	if err := os.Remove(systemdRuntimeDropInFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", systemdRuntimeDropInFile, err)
	}
	// Best-effort rmdir of the drop-in directory (ignore errors — it may
	// contain other drop-ins the user added).
	_ = os.Remove(systemdRuntimeDropInDir)
	return runSystemctl("daemon-reload")
}

// runSystemctl runs `systemctl <args...>` with sudo when the process isn't
// already running as root. Stdout/stderr are wired through so the user can
// see systemctl's error messages if the operation fails.
func runSystemctl(args ...string) error {
	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.Command("systemctl", args...) //nolint:gosec // G204: args come from compile-time constants
	} else {
		full := append([]string{"systemctl"}, args...)
		cmd = exec.Command("sudo", full...) //nolint:gosec // G204: args come from compile-time constants
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// versionAtLeast returns true if semver `a` is greater than or equal to `b`.
// Only supports dotted numeric semver (major.minor.patch) without pre-release
// suffixes — sufficient for "is this version at least 2.1.0" style checks.
func versionAtLeast(a, b string) bool {
	aParts := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bParts := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range 3 {
		ai, bi := 0, 0
		if i < len(aParts) {
			ai, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bi, _ = strconv.Atoi(bParts[i])
		}
		if ai > bi {
			return true
		}
		if ai < bi {
			return false
		}
	}
	return true
}
