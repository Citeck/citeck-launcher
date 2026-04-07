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

	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update the launcher binary to the latest version",
		Long:  "Check for and install the latest version from GitHub Releases.",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if check {
				return nil
			}

			// Find matching asset
			assetName := fmt.Sprintf("citeck_%s_%s_%s", latestVersion, runtime.GOOS, runtime.GOARCH)
			var downloadURL string
			for _, a := range release.Assets {
				if a.Name == assetName {
					downloadURL = a.BrowserDownloadURL
					break
				}
			}
			if downloadURL == "" {
				return fmt.Errorf("no binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
			}

			// Find checksums
			var checksumsURL string
			for _, a := range release.Assets {
				if a.Name == "checksums.txt" {
					checksumsURL = a.BrowserDownloadURL
					break
				}
			}

			// Download binary to temp file next to executable (same FS for atomic rename)
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}
			tmpPath := executable + ".update-tmp"

			output.PrintText("Downloading %s...", assetName)
			hash, err := downloadFile(downloadURL, tmpPath)
			if err != nil {
				_ = os.Remove(tmpPath)
				return fmt.Errorf("download: %w", err)
			}

			// Verify checksum if available
			if checksumsURL != "" {
				if verifyErr := verifyChecksum(checksumsURL, assetName, hash); verifyErr != nil {
					_ = os.Remove(tmpPath)
					return verifyErr
				}
				output.PrintText("Checksum verified")
			}

			// Make executable
			if chmodErr := os.Chmod(tmpPath, 0o755); chmodErr != nil { //nolint:gosec // binary needs 0o755
				_ = os.Remove(tmpPath)
				return fmt.Errorf("chmod: %w", chmodErr)
			}

			// Atomic replace
			if renameErr := os.Rename(tmpPath, executable); renameErr != nil {
				_ = os.Remove(tmpPath)
				return fmt.Errorf("replace binary: %w (try running with sudo)", renameErr)
			}

			output.PrintText("Updated to v%s", latestVersion)

			// Offer systemd restart if service is active
			if isSystemdActive("citeck") {
				if flagYes || confirmPrompt("Restart citeck service? [Y/n]: ") {
					output.PrintText("Restarting service...")
					if restartErr := exec.Command("systemctl", "restart", "citeck").Run(); restartErr != nil { //nolint:gosec // trusted command
						output.Errf("Failed to restart: %v. Run manually: sudo systemctl restart citeck", restartErr)
					} else {
						output.PrintText("Service restarted")
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Only check for updates, don't install")
	return cmd
}

func fetchLatestRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(githubAPIBase + "/releases/latest") //nolint:gosec // trusted URL
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
