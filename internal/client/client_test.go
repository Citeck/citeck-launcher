package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTransport_HostFlag(t *testing.T) {
	tc, err := DetectTransport("prod.example.com:8088", "mytoken")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Type != TransportTCP {
		t.Error("expected TCP transport when --host is set")
	}
	if tc.Host != "prod.example.com:8088" {
		t.Errorf("expected host prod.example.com:8088, got %s", tc.Host)
	}
	if tc.Token != "mytoken" {
		t.Errorf("expected token mytoken, got %s", tc.Token)
	}
}

func TestDetectTransport_EnvHost(t *testing.T) {
	t.Setenv("CITECK_HOST", "staging.co:8088")
	t.Setenv("CITECK_TOKEN", "envtoken")

	tc, err := DetectTransport("", "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Type != TransportTCP {
		t.Error("expected TCP transport via CITECK_HOST env")
	}
	if tc.Host != "staging.co:8088" {
		t.Errorf("expected host staging.co:8088, got %s", tc.Host)
	}
	if tc.Token != "envtoken" {
		t.Errorf("expected token envtoken, got %s", tc.Token)
	}
}

func TestDetectTransport_UnixSocket(t *testing.T) {
	// Create a temporary socket file
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "") // clear

	tc, err := DetectTransport("", "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Type != TransportUnix {
		t.Error("expected Unix socket transport")
	}
	if tc.SocketPath != sockPath {
		t.Errorf("expected socket path %s, got %s", sockPath, tc.SocketPath)
	}
}

func TestDetectTransport_NoDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	_, err := DetectTransport("", "")
	if err == nil {
		t.Error("expected error when no daemon is running")
	}
}

func TestDetectTransport_HostFlagOverridesEnv(t *testing.T) {
	t.Setenv("CITECK_HOST", "env-host:8088")

	tc, err := DetectTransport("flag-host:8088", "")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Host != "flag-host:8088" {
		t.Errorf("expected flag host to override env, got %s", tc.Host)
	}
}

func TestBaseURL_Unix(t *testing.T) {
	tc := &TransportConfig{Type: TransportUnix}
	if tc.BaseURL() != "http://localhost" {
		t.Errorf("expected http://localhost, got %s", tc.BaseURL())
	}
}

func TestBaseURL_TCP(t *testing.T) {
	tc := &TransportConfig{Type: TransportTCP, Host: "prod.co:8088"}
	if tc.BaseURL() != "http://prod.co:8088" {
		t.Errorf("expected http://prod.co:8088, got %s", tc.BaseURL())
	}
}
