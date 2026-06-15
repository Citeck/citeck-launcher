package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDaemonConfig(t *testing.T) {
	cfg := DefaultDaemonConfig()
	// The Web UI is OFF by default: not released for server use (CLI/TUI is
	// the supported interface) and the TCP listener serves the privileged
	// API, so it must be an explicit opt-in via daemon.yml.
	if cfg.Server.WebUI.Enabled {
		t.Error("Default WebUI.Enabled should be false (server web UI is opt-in)")
	}
	if cfg.Server.WebUI.Listen != "127.0.0.1:7088" {
		t.Errorf("Default WebUI.Listen = %q, want 127.0.0.1:7088", cfg.Server.WebUI.Listen)
	}
	// API token auth is strictly opt-in: default OFF and no baked-in token,
	// so existing localhost flows keep working unchanged.
	if cfg.APIAuth.Enabled {
		t.Error("Default APIAuth.Enabled should be false")
	}
	if cfg.APIAuth.Token != "" {
		t.Errorf("Default APIAuth.Token should be empty, got %q", cfg.APIAuth.Token)
	}
}

func TestLoadDaemonConfig_APIAuthDefaultsOffWhenAbsent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)
	SetDesktopMode(false)

	// A daemon.yml without an api_auth block must load with auth disabled.
	if err := os.MkdirAll(ConfDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "server:\n  webui:\n    enabled: true\n    listen: 127.0.0.1:7088\n"
	if err := os.WriteFile(DaemonConfigPath(), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error: %v", err)
	}
	if cfg.APIAuth.Enabled {
		t.Error("api_auth absent from daemon.yml should mean disabled")
	}
}

func TestLoadDaemonConfig_APIAuthParsed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)
	SetDesktopMode(false)

	if err := os.MkdirAll(ConfDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "server:\n  webui:\n    enabled: true\n    listen: 127.0.0.1:7088\napi_auth:\n  enabled: true\n  token: my-secret-token\n"
	if err := os.WriteFile(DaemonConfigPath(), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error: %v", err)
	}
	if !cfg.APIAuth.Enabled {
		t.Error("api_auth.enabled: true should parse as enabled")
	}
	if cfg.APIAuth.Token != "my-secret-token" {
		t.Errorf("APIAuth.Token = %q, want my-secret-token", cfg.APIAuth.Token)
	}
}

func TestEnsureAPIToken_ExplicitTokenWins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)
	SetDesktopMode(false)

	cfg := DefaultDaemonConfig()
	cfg.APIAuth = APIAuthConfig{Enabled: true, Token: "explicit"}
	token, generated, err := EnsureAPIToken(cfg)
	if err != nil {
		t.Fatalf("EnsureAPIToken() error: %v", err)
	}
	if token != "explicit" || generated {
		t.Errorf("EnsureAPIToken() = (%q, %v), want (explicit, false)", token, generated)
	}
	if _, statErr := os.Stat(APITokenPath()); !os.IsNotExist(statErr) {
		t.Error("explicit token must not create the token file")
	}
}

func TestEnsureAPIToken_GeneratesPersistsAndReuses(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)
	SetDesktopMode(false)

	cfg := DefaultDaemonConfig()
	cfg.APIAuth = APIAuthConfig{Enabled: true}

	token, generated, err := EnsureAPIToken(cfg)
	if err != nil {
		t.Fatalf("EnsureAPIToken() error: %v", err)
	}
	if !generated {
		t.Error("first call should generate a token")
	}
	if len(token) != 64 { // 32 random bytes, hex-encoded
		t.Errorf("generated token length = %d, want 64", len(token))
	}
	info, statErr := os.Stat(APITokenPath())
	if statErr != nil {
		t.Fatalf("token file not created: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 0600", perm)
	}

	// Second call must return the SAME token without regenerating —
	// browsers/scripts already hold it.
	again, generatedAgain, err := EnsureAPIToken(cfg)
	if err != nil {
		t.Fatalf("EnsureAPIToken() second call error: %v", err)
	}
	if generatedAgain {
		t.Error("second call must not regenerate")
	}
	if again != token {
		t.Errorf("second call token = %q, want %q (stable)", again, token)
	}

	// LoadAPIToken (CLI read path) sees the same token.
	loaded, err := LoadAPIToken(cfg)
	if err != nil {
		t.Fatalf("LoadAPIToken() error: %v", err)
	}
	if loaded != token {
		t.Errorf("LoadAPIToken() = %q, want %q", loaded, token)
	}
}

func TestLoadAPIToken_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CITECK_HOME", tmp)
	SetDesktopMode(false)

	cfg := DefaultDaemonConfig()
	cfg.APIAuth = APIAuthConfig{Enabled: true}
	if _, err := LoadAPIToken(cfg); err == nil {
		t.Error("LoadAPIToken() with no token file should error (CLI must never mint a token)")
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
	if cfg.Server.WebUI.Enabled {
		t.Error("Missing config should return defaults (web UI disabled)")
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

// TestLoadDaemonConfig_ExplicitWebUIEnableHonored: flipping the default to
// disabled must not strand servers that already opted in — an explicit
// `server.webui.enabled: true` in daemon.yml keeps the UI on.
func TestLoadDaemonConfig_ExplicitWebUIEnableHonored(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("CITECK_HOME", tmp)
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(false)

	if err := os.MkdirAll(filepath.Join(tmp, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "server:\n  webui:\n    enabled: true\n    listen: 127.0.0.1:7088\n"
	if err := os.WriteFile(filepath.Join(tmp, "conf", "daemon.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error: %v", err)
	}
	if !cfg.Server.WebUI.Enabled {
		t.Error("explicit enabled: true in daemon.yml must keep the web UI on")
	}
}
