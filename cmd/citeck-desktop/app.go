package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
)

// dumpSystemInfo downloads a diagnostic ZIP from the daemon via Unix socket.
func dumpSystemInfo(socketPath string) {
	homeDir := config.HomeDir()
	ts := time.Now().Format("20060102-150405")
	zipPath := filepath.Join(homeDir, fmt.Sprintf("system-dump-%s.zip", ts))

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", socketPath, 5*time.Second)
			},
		},
	}

	resp, err := client.Get("http://localhost/api/v1/system/dump?format=zip")
	if err != nil {
		slog.Error("Failed to get system dump from daemon", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("System dump returned error", "status", resp.StatusCode)
		return
	}

	f, err := os.Create(zipPath) //nolint:gosec // path is constructed from homeDir + timestamp, not user input
	if err != nil {
		slog.Error("Failed to create dump file", "path", zipPath, "err", err)
		return
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		slog.Error("Failed to write dump file", "err", err)
		return
	}

	slog.Info("System dump created", "path", zipPath)
}
