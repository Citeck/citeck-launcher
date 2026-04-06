package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/output"
	"github.com/spf13/cobra"
)

type diagnoseCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warning, error
	Message string `json:"message"`
	Fixable bool   `json:"fixable"`
}

func newDiagnoseCmd() *cobra.Command {
	var fix bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Find and optionally fix problems",
		RunE: func(cmd *cobra.Command, args []string) error {
			var checks []diagnoseCheck

			// Check 1: Socket file
			socketPath := config.SocketPath()
			if _, err := os.Stat(socketPath); err == nil { //nolint:nestif // diagnostic checks have inherent branching
				// Socket exists — check if daemon is listening
				conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
				if err != nil {
					checks = append(checks, diagnoseCheck{
						Name:    "stale_socket",
						Status:  "error",
						Message: fmt.Sprintf("Stale socket file at %s (daemon not responding)", socketPath),
						Fixable: true,
					})
					if fix && !dryRun {
						_ = os.Remove(socketPath)
						checks[len(checks)-1].Message += " — FIXED (removed)"
						checks[len(checks)-1].Status = "ok"
					}
				} else {
					_ = conn.Close()
					checks = append(checks, diagnoseCheck{
						Name: "socket", Status: "ok", Message: "Daemon socket is responsive",
					})
				}
			} else {
				checks = append(checks, diagnoseCheck{
					Name: "socket", Status: "warning", Message: "No daemon socket found (daemon not running)",
				})
			}

			// Check 2: Config file
			cfgPath := config.NamespaceConfigPath()
			if _, err := os.Stat(cfgPath); err != nil {
				checks = append(checks, diagnoseCheck{
					Name: "config", Status: "warning", Message: fmt.Sprintf("No config file at %s", cfgPath),
				})
			} else {
				checks = append(checks, diagnoseCheck{
					Name: "config", Status: "ok", Message: "Config file exists",
				})
			}

			// Check 3: Docker (uses Docker SDK auto-detection, respects DOCKER_HOST)
			dockerClient, dockerErr := docker.NewClient("diagnose")
			if dockerErr != nil {
				checks = append(checks, diagnoseCheck{
					Name: "docker", Status: "error", Message: "Docker client error: " + dockerErr.Error(),
				})
			} else {
				pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := dockerClient.Ping(pingCtx); err != nil {
					checks = append(checks, diagnoseCheck{
						Name: "docker", Status: "error", Message: "Docker is not reachable: " + err.Error(),
					})
				} else {
					checks = append(checks, diagnoseCheck{
						Name: "docker", Status: "ok", Message: "Docker is reachable",
					})
				}
				pingCancel()
				dockerClient.Close()
			}

			// Check 4: Directories
			for _, dir := range []string{config.ConfDir(), config.DataDir(), config.LogDir()} {
				if _, err := os.Stat(dir); err != nil {
					checks = append(checks, diagnoseCheck{
						Name:    "directory",
						Status:  "error",
						Message: fmt.Sprintf("Missing directory: %s", dir),
						Fixable: true,
					})
					if fix && !dryRun {
						os.MkdirAll(dir, 0o755) //nolint:gosec // G301: data dir needs 0o755
						checks[len(checks)-1].Message += " — FIXED (created)"
						checks[len(checks)-1].Status = "ok"
					}
				}
			}

			// Check 5: Port conflicts
			checkPort := func(port int, name string) {
				ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
				if err != nil {
					checks = append(checks, diagnoseCheck{
						Name: "port", Status: "warning", Message: fmt.Sprintf("Port %d (%s) is in use", port, name),
					})
				} else {
					ln.Close()
					checks = append(checks, diagnoseCheck{
						Name: "port", Status: "ok", Message: fmt.Sprintf("Port %d (%s) is available", port, name),
					})
				}
			}
			checkPort(80, "HTTP")
			checkPort(443, "HTTPS")

			// Output
			result := map[string]any{
				"checks": checks,
			}

			errCount := 0
			for _, c := range checks {
				if c.Status == "error" {
					errCount++
				}
			}

			output.PrintResult(result, func() {
				for _, c := range checks {
					icon := formatCheckIcon(c.Status)
					msg := c.Message
					if c.Fixable && !fix {
						msg += " (fixable with --fix)"
					}
					output.PrintText("  %s  %s", icon, msg)
				}
				fmt.Println()
				if errCount > 0 {
					output.PrintText("%d problem(s) found", errCount)
				} else {
					output.PrintText(output.Colorize(output.Green, "No problems found"))
				}
			})

			if errCount > 0 {
				return exitWithCode(ExitError, "%d problem(s) found", errCount)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Auto-fix fixable problems")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview fixes without applying")

	return cmd
}
