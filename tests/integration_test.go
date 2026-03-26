//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
)

func skipIfNoDaemon(t *testing.T) *client.DaemonClient {
	t.Helper()
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		t.Skipf("daemon not running (no socket at %s)", socketPath)
	}
	c, err := client.New("", "")
	if err != nil {
		t.Skipf("cannot connect to daemon: %v", err)
	}
	return c
}

func TestIntegration_DaemonStatus(t *testing.T) {
	c := skipIfNoDaemon(t)
	defer c.Close()

	status, err := c.GetStatus()
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if !status.Running {
		t.Error("daemon reports not running")
	}
	if status.PID <= 0 {
		t.Error("expected positive PID")
	}

	// Verify JSON roundtrip
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed api.DaemonStatusDto
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Running != status.Running {
		t.Error("roundtrip failed")
	}
}

func TestIntegration_Namespace(t *testing.T) {
	c := skipIfNoDaemon(t)
	defer c.Close()

	ns, err := c.GetNamespace()
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}
	if ns.Name == "" {
		t.Error("namespace name should not be empty")
	}
	t.Logf("Namespace: %s, Status: %s, Apps: %d", ns.Name, ns.Status, len(ns.Apps))

	for _, app := range ns.Apps {
		if app.Name == "" {
			t.Error("app name should not be empty")
		}
		t.Logf("  %s: %s (%s)", app.Name, app.Status, app.Image)
	}
}

func TestIntegration_Health(t *testing.T) {
	c := skipIfNoDaemon(t)
	defer c.Close()

	health, err := c.GetHealth()
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	t.Logf("Healthy: %v, Checks: %d", health.Healthy, len(health.Checks))

	for _, check := range health.Checks {
		t.Logf("  %s: %s — %s", check.Name, check.Status, check.Message)
	}
}

func TestIntegration_StatusJSON(t *testing.T) {
	c := skipIfNoDaemon(t)
	defer c.Close()

	ns, err := c.GetNamespace()
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	// Verify JSON serialization produces valid output
	data, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		t.Fatalf("marshal namespace: %v", err)
	}

	var parsed api.NamespaceDto
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal namespace: %v", err)
	}
	if parsed.Name != ns.Name {
		t.Errorf("name mismatch: %s != %s", parsed.Name, ns.Name)
	}
	if len(parsed.Apps) != len(ns.Apps) {
		t.Errorf("apps count mismatch: %d != %d", len(parsed.Apps), len(ns.Apps))
	}
}
