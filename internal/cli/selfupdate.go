package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

const (
	githubRepo    = "citeck/citeck-launcher"
	githubAPIBase = "https://api.github.com/repos/" + githubRepo
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func newSelfUpdateCmd(currentVersion string) *cobra.Command {
	var check bool
	var file string

	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update the launcher binary to the latest version",
		Long: `Check for and install the latest version from GitHub Releases.
Use --file to install from a local binary (offline environments).
Use "citeck self-update rollback" to revert to the previous version.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				return selfUpdateFromFile(file)
			}
			return selfUpdateFromGitHub(currentVersion, check)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Only check for updates, don't install")
	cmd.Flags().StringVar(&file, "file", "", "Install from a local binary file (offline update)")
	cmd.AddCommand(newRollbackCmd())
	return cmd
}

func newRollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Revert to the previous launcher version",
		RunE: func(cmd *cobra.Command, args []string) error {
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}
			backupPath := executable + ".prev"

			if _, statErr := os.Stat(backupPath); statErr != nil {
				return fmt.Errorf("no previous version found at %s", backupPath)
			}

			daemonRunning := isDaemonRunning()

			if !flagYes {
				if daemonRunning {
					output.PrintText("\nThis will: stop daemon → restore previous binary → start daemon")
				} else {
					output.PrintText("\nThis will: restore %s from %s", executable, backupPath)
				}
				if !confirmPrompt("Proceed? [Y/n]: ") {
					output.PrintText("Canceled")
					return nil
				}
			}

			if daemonRunning {
				stopDaemonForUpdate()
			}

			// Swap: current → .rollback-tmp, prev → current, .rollback-tmp → prev
			tmpPath := executable + ".rollback-tmp"
			if renameErr := os.Rename(executable, tmpPath); renameErr != nil {
				return fmt.Errorf("move current binary: %w", renameErr)
			}
			if renameErr := os.Rename(backupPath, executable); renameErr != nil {
				// Try to restore
				_ = os.Rename(tmpPath, executable)
				return fmt.Errorf("restore previous binary: %w", renameErr)
			}
			// Current becomes the new backup
			_ = os.Rename(tmpPath, backupPath)

			output.PrintText("Rolled back to previous version")

			if daemonRunning {
				startDaemonAfterUpdate()
			}

			return nil
		},
	}
}

func selfUpdateFromGitHub(currentVersion string, checkOnly bool) error {
	if currentVersion == "dev" || currentVersion == "" {
		return fmt.Errorf("cannot self-update a development build — install a release binary first")
	}

	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	if currentVersion == latestVersion {
		output.PrintText("Already up to date (v%s)", currentVersion)
		return nil
	}

	output.PrintText("Current: v%s → Latest: v%s", currentVersion, latestVersion)

	if checkOnly {
		return nil
	}

	// Find matching asset
	assetName := fmt.Sprintf("citeck_%s_%s_%s", latestVersion, runtime.GOOS, runtime.GOARCH)
	asset, checksumsAsset := findReleaseAssets(release, assetName)
	if asset == nil {
		return fmt.Errorf("no binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	daemonRunning := isDaemonRunning()

	if !confirmUpdate(executable, daemonRunning) {
		return nil
	}

	// Download
	tmpPath := executable + ".update-tmp"
	output.PrintText("Downloading %s...", assetName)
	hash, dlErr := downloadFile(asset.BrowserDownloadURL, tmpPath)
	if dlErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download: %w", dlErr)
	}

	// Verify checksum
	if checksumsAsset != nil {
		if verifyErr := verifyChecksum(checksumsAsset.BrowserDownloadURL, assetName, hash); verifyErr != nil {
			_ = os.Remove(tmpPath)
			return verifyErr
		}
		output.PrintText("Checksum verified")
	}

	return replaceBinary(tmpPath, executable, daemonRunning, latestVersion)
}

func selfUpdateFromFile(filePath string) error {
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("file not found: %s", filePath)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	daemonRunning := isDaemonRunning()

	if !confirmUpdate(executable, daemonRunning) {
		return nil
	}

	// Copy file to temp location next to executable (same FS for atomic rename)
	tmpPath := executable + ".update-tmp"
	src, err := os.Open(filePath) //nolint:gosec // user-specified path
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer src.Close()
	dst, err := os.Create(tmpPath) //nolint:gosec // temp file
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, copyErr := io.Copy(dst, src); copyErr != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copy binary: %w", copyErr)
	}
	if closeErr := dst.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("flush: %w", closeErr)
	}

	return replaceBinary(tmpPath, executable, daemonRunning, "")
}

func findReleaseAssets(release *githubRelease, assetName string) (binary, checksums *githubAsset) {
	for i := range release.Assets {
		switch release.Assets[i].Name {
		case assetName:
			binary = &release.Assets[i]
		case "checksums.txt":
			checksums = &release.Assets[i]
		}
	}
	return
}

func isDaemonRunning() bool {
	if c := client.TryNew(clientOpts()); c != nil {
		c.Close()
		return true
	}
	return false
}

func confirmUpdate(executable string, daemonRunning bool) bool {
	if flagYes {
		return true
	}
	if daemonRunning {
		output.PrintText("\nThis will: stop daemon → replace binary → start daemon")
	} else {
		output.PrintText("\nThis will: replace binary %s", executable)
	}
	return confirmPrompt("Proceed? [Y/n]: ")
}

// replaceBinary sets permissions, backs up current binary, stops daemon, replaces, and restarts.
func replaceBinary(tmpPath, executable string, daemonRunning bool, version string) error {
	if chmodErr := os.Chmod(tmpPath, 0o755); chmodErr != nil { //nolint:gosec // binary needs 0o755
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", chmodErr)
	}

	if daemonRunning {
		stopDaemonForUpdate()
	}

	// Back up current binary for rollback
	backupPath := executable + ".prev"
	if copyErr := copyBinary(executable, backupPath); copyErr != nil {
		output.Errf("Warning: could not back up current binary: %v", copyErr)
	} else {
		output.PrintText("Previous version saved to %s", backupPath)
	}

	if renameErr := os.Rename(tmpPath, executable); renameErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace binary: %w (try running with sudo)", renameErr)
	}

	if version != "" {
		output.PrintText("Updated to v%s", version)
	} else {
		output.PrintText("Binary replaced from file")
	}

	if daemonRunning {
		startDaemonAfterUpdate()
	}

	return nil
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // our own binary
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst) //nolint:gosec // backup path
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, cpErr := io.Copy(out, in); cpErr != nil {
		_ = out.Close()
		return fmt.Errorf("copy: %w", cpErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return fmt.Errorf("flush %s: %w", dst, closeErr)
	}
	if chmodErr := os.Chmod(dst, 0o755); chmodErr != nil { //nolint:gosec // binary needs 0o755
		return fmt.Errorf("chmod %s: %w", dst, chmodErr)
	}
	return nil
}

func stopDaemonForUpdate() {
	output.PrintText("Stopping daemon...")
	c, err := client.New(clientOpts())
	if err != nil {
		output.Errf("Warning: could not connect to daemon: %v", err)
		return
	}
	defer c.Close()
	if _, stopErr := c.StopNamespace(); stopErr != nil {
		output.Errf("Warning: stop namespace: %v", stopErr)
	}
	if waitErr := waitForStopped(c, 120*time.Second); waitErr != nil {
		output.Errf("Warning: %v — proceeding anyway", waitErr)
	}
	if _, shutErr := c.Shutdown(); shutErr != nil {
		output.Errf("Warning: shutdown daemon: %v", shutErr)
	}
	time.Sleep(2 * time.Second)
	output.PrintText("Daemon stopped")
}

func startDaemonAfterUpdate() {
	if isSystemdActive("citeck") {
		output.PrintText("Starting service...")
		if err := exec.Command("systemctl", "start", "citeck").Run(); err != nil { //nolint:gosec // trusted command
			output.Errf("Failed to start service: %v. Run: sudo systemctl start citeck", err)
		} else {
			output.PrintText("Service started")
		}
	} else {
		output.PrintText("Daemon was running but not via systemd. Start manually: citeck start")
	}
}

func fetchLatestRelease() (*githubRelease, error) {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Get(githubAPIBase + "/releases/latest") //nolint:gosec // trusted URL
	if err != nil {
		return nil, fmt.Errorf("GitHub API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if decodeErr := json.NewDecoder(resp.Body).Decode(&release); decodeErr != nil {
		return nil, fmt.Errorf("parse release: %w", decodeErr)
	}
	return &release, nil
}

const maxBinarySize = 256 * 1024 * 1024  // 256 MB
const maxChecksumsSize = 1024 * 1024     // 1 MB

// downloadFile downloads url to dst and returns the SHA256 hex hash.
func downloadFile(url, dst string) (string, error) {
	resp, err := http.Get(url) //nolint:gosec // URL from GitHub API
	if err != nil {
		return "", fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dst) //nolint:gosec // temp file path from our executable dir
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer out.Close()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxBinarySize)
	if _, copyErr := io.Copy(out, io.TeeReader(limited, hasher)); copyErr != nil {
		return "", fmt.Errorf("write: %w", copyErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return "", fmt.Errorf("flush: %w", closeErr)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func verifyChecksum(checksumsURL, assetName, actualHash string) error {
	resp, err := http.Get(checksumsURL) //nolint:gosec // URL from GitHub API
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumsSize))
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			if parts[0] != actualHash {
				return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
			}
			return nil
		}
	}
	// Asset not in checksums file — skip verification
	return nil
}

func isSystemdActive(service string) bool {
	err := exec.Command("systemctl", "is-active", "--quiet", service).Run()
	return err == nil
}

func confirmPrompt(prompt string) bool {
	fmt.Print(prompt) //nolint:forbidigo // CLI prompt
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false // EOF
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "" || answer == "y" || answer == "yes"
}
