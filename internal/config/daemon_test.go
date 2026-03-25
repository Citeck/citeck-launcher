package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDaemonConfig(t *testing.T) {
	cfg := DefaultDaemonConfig()
	if !cfg.Server.WebUI.Enabled {
		t.Error("Default WebUI.Enabled should be true")
	}
	if cfg.Server.WebUI.Listen != "127.0.0.1:8088" {
		t.Errorf("Default WebUI.Listen = %q, want 127.0.0.1:8088", cfg.Server.WebUI.Listen)
	}
}

func TestLoadDaemonConfigMissing(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(false)

	cfg, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error: %v", err)
	}
	if !cfg.Server.WebUI.Enabled {
		t.Error("Missing config should return defaults (enabled=true)")
	}
}

func TestSaveAndLoadDaemonConfig(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(false)

	// Save custom config
	cfg := DaemonConfig{
		Server: ServerConfig{
			WebUI: WebUIConfig{
				Enabled: false,
				Listen:  "0.0.0.0:9090",
			},
		},
	}
	if err := SaveDaemonConfig(cfg); err != nil {
		t.Fatalf("SaveDaemonConfig() error: %v", err)
	}

	// Verify file exists
	path := filepath.Join(tmp, "conf", "daemon.yml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("daemon.yml not created: %v", err)
	}

	// Load and verify
	loaded, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error: %v", err)
	}
	if loaded.Server.WebUI.Enabled {
		t.Error("Loaded WebUI.Enabled should be false")
	}
	if loaded.Server.WebUI.Listen != "0.0.0.0:9090" {
		t.Errorf("Loaded WebUI.Listen = %q, want 0.0.0.0:9090", loaded.Server.WebUI.Listen)
	}
}
