package cli

import (
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
Use --file to install from a local binary (offline environments).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				return selfUpdateFromFile(file)
			}
			return selfUpdateFromGitHub(currentVersion, check)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Only check for updates, don't install")
	cmd.Flags().StringVar(&file, "file", "", "Install from a local binary file (offline update)")
	return cmd
}

func selfUpdateFromGitHub(currentVersion string, checkOnly bool) error {
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

// replaceBinary sets permissions, stops daemon, atomically replaces the binary, and restarts.
func replaceBinary(tmpPath, executable string, daemonRunning bool, version string) error {
	if chmodErr := os.Chmod(tmpPath, 0o755); chmodErr != nil { //nolint:gosec // binary needs 0o755
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", chmodErr)
	}

	if daemonRunning {
		stopDaemonForUpdate()
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

func stopDaemonForUpdate() {
	output.PrintText("Stopping daemon...")
	c, err := client.New(clientOpts())
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.StopNamespace()
	_ = waitForStopped(c, 120*time.Second)
	_, _ = c.Shutdown()
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
	if _, copyErr := io.Copy(out, io.TeeReader(resp.Body, hasher)); copyErr != nil {
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
	body, err := io.ReadAll(resp.Body)
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
	buf := make([]byte, 64)
	n, _ := os.Stdin.Read(buf)
	answer := strings.TrimSpace(strings.ToLower(string(buf[:n])))
	return answer == "" || answer == "y" || answer == "yes"
}
