package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// --- Diagnostics ---

func (d *Daemon) handleGetDiagnostics(w http.ResponseWriter, _ *http.Request) {
	var checks []api.DiagnosticCheckDto

	// Check 1: Docker reachable
	if d.dockerClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pingErr := d.dockerClient.Ping(ctx)
		cancel()
		if pingErr != nil {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Docker", Status: "error", Message: "Docker is not reachable: " + pingErr.Error(), Fixable: false,
			})
		} else {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Docker", Status: "ok", Message: "Docker is running", Fixable: false,
			})
		}
	}

	// Check 2: Socket exists
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Socket", Status: "error", Message: "Daemon socket not found: " + socketPath, Fixable: true,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Socket", Status: "ok", Message: "Socket: " + socketPath, Fixable: false,
		})
	}

	// Check 3: Namespace config valid
	nsID := d.activeNsID()
	nsCfgPath := config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
	if _, err := namespace.LoadNamespaceConfig(nsCfgPath); err != nil {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Config", Status: "warning", Message: "Namespace config: " + err.Error(), Fixable: false,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Config", Status: "ok", Message: "Namespace config is valid", Fixable: false,
		})
	}

	// Check 4: Disk space
	if freeGB, totalGB, err := diskSpace(config.HomeDir()); err == nil {
		pctUsed := 0.0
		if totalGB > 0 {
			pctUsed = (1 - freeGB/totalGB) * 100
		}
		msg := fmt.Sprintf("Disk: %.1f GB free of %.1f GB (%.0f%% used)", freeGB, totalGB, pctUsed)
		status := "ok"
		if freeGB < 1.0 {
			status = "error"
			msg = fmt.Sprintf("Disk critically low: %.1f GB free", freeGB)
		} else if freeGB < 5.0 {
			status = "warning"
			msg = fmt.Sprintf("Disk space low: %.1f GB free of %.1f GB", freeGB, totalGB)
		}
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: status, Message: msg, Fixable: false,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: "warning", Message: "Disk space check failed: " + err.Error(), Fixable: false,
		})
	}

	// Check 5: Runtime status
	if d.runtime != nil {
		status := d.runtime.Status()
		if status == namespace.NsStatusRunning {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Runtime", Status: "ok", Message: "Namespace is running", Fixable: false,
			})
		} else {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Runtime", Status: "warning", Message: fmt.Sprintf("Namespace status: %s", status), Fixable: false,
			})
		}
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Runtime", Status: "warning", Message: "No namespace is loaded", Fixable: false,
		})
	}

	writeJSON(w, api.DiagnosticsDto{Checks: checks})
}

//nolint:nestif // fix logic requires checking socket state with nested error handling
func (d *Daemon) handleDiagnosticsFix(w http.ResponseWriter, _ *http.Request) {
	fixed := 0
	failed := 0

	// Fix 1: stale socket cleanup
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err == nil {
		// Check if socket is actually in use by trying to connect
		conn, dialErr := net.DialTimeout("unix", socketPath, 2*time.Second)
		if dialErr != nil {
			// Socket exists but nobody is listening — it's stale
			if err := os.Remove(socketPath); err != nil {
				failed++
			} else {
				fixed++
			}
		} else {
			_ = conn.Close()
		}
	}

	msg := fmt.Sprintf("Fixed %d issue(s)", fixed)
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	writeJSON(w, api.DiagFixResultDto{Fixed: fixed, Failed: failed, Message: msg})
}
