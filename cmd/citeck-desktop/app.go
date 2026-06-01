//go:build desktop

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
)

// dumpSystemInfo downloads a diagnostic ZIP from the daemon via Unix socket
// and returns the on-disk path. The caller (tray handler) is responsible for
// surfacing UX (folder-open, toast, error dialog) so the menu item can react
// uniformly to success and failure.
func dumpSystemInfo(socketPath string) (string, error) {
	// Write into a dedicated reports/ subfolder rather than the launcher root,
	// so dumps don't clutter the home dir next to launcher.db / storage.db /
	// conf (Kotlin 1.x parity: SystemDumpUtils wrote under AppDir/reports).
	reportsDir := filepath.Join(config.HomeDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o750); err != nil {
		return "", fmt.Errorf("create reports dir: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	zipPath := filepath.Join(reportsDir, fmt.Sprintf("system-dump-%s.zip", ts))

	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", socketPath, 5*time.Second)
			},
		},
	}

	resp, err := client.Get("http://localhost/api/v1/system/dump?format=zip")
	if err != nil {
		return "", fmt.Errorf("request daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}

	f, err := os.Create(zipPath) //nolint:gosec // path is constructed from homeDir + timestamp, not user input
	if err != nil {
		return "", fmt.Errorf("create dump file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("write dump file: %w", err)
	}

	return zipPath, nil
}
