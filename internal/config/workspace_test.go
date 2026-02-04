package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListWorkspacesEmpty(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")

	workspaces, err := ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces() error: %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("Expected 0 workspaces, got %d", len(workspaces))
	}
}

func TestListWorkspacesWithDirs(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")

	// Create workspace dirs matching Kotlin structure
	os.MkdirAll(filepath.Join(tmp, "ws", "default", "ns", "prod"), 0o755)
	os.MkdirAll(filepath.Join(tmp, "ws", "default", "ns", "dev"), 0o755)
	os.MkdirAll(filepath.Join(tmp, "ws", "custom", "ns", "staging"), 0o755)

	workspaces, err := ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces() error: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("Expected 2 workspaces, got %d", len(workspaces))
	}

	// Build map for easier assertions
	wsMap := make(map[string]WorkspaceInfo)
	for _, ws := range workspaces {
		wsMap[ws.ID] = ws
	}

	if ws, ok := wsMap["default"]; !ok {
		t.Error("Missing workspace 'default'")
	} else if len(ws.Namespaces) != 2 {
		t.Errorf("Workspace 'default' should have 2 namespaces, got %d", len(ws.Namespaces))
	}

	if ws, ok := wsMap["custom"]; !ok {
		t.Error("Missing workspace 'custom'")
	} else if len(ws.Namespaces) != 1 {
		t.Errorf("Workspace 'custom' should have 1 namespace, got %d", len(ws.Namespaces))
	}
}

func TestListAllNamespaces(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")

	os.MkdirAll(filepath.Join(tmp, "ws", "default", "ns", "prod"), 0o755)
	os.MkdirAll(filepath.Join(tmp, "ws", "default", "ns", "dev"), 0o755)

	namespaces, err := ListAllNamespaces()
	if err != nil {
		t.Fatalf("ListAllNamespaces() error: %v", err)
	}
	if len(namespaces) != 2 {
		t.Fatalf("Expected 2 namespaces, got %d", len(namespaces))
	}

	for _, ns := range namespaces {
		if ns.WorkspaceID != "default" {
			t.Errorf("WorkspaceID = %q, want 'default'", ns.WorkspaceID)
		}
		expectedConfig := filepath.Join(tmp, "ws", "default", "ns", ns.NamespaceID, "namespace.yml")
		if ns.ConfigPath != expectedConfig {
			t.Errorf("ConfigPath = %q, want %q", ns.ConfigPath, expectedConfig)
		}
		expectedRt := filepath.Join(tmp, "ws", "default", "ns", ns.NamespaceID, "rtfiles")
		if ns.RtfilesDir != expectedRt {
			t.Errorf("RtfilesDir = %q, want %q", ns.RtfilesDir, expectedRt)
		}
	}
}
