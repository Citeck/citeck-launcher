package client

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTransport_HostFlag(t *testing.T) {
	tc, err := DetectTransport("prod.example.com:7088", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Type != TransportTCP {
		t.Error("expected TCP transport when --host is set")
	}
	if tc.Host != "prod.example.com:7088" {
		t.Errorf("expected host prod.example.com:7088, got %s", tc.Host)
	}
}

func TestDetectTransport_EnvHost(t *testing.T) {
	t.Setenv("CITECK_HOST", "staging.co:7088")

	tc, err := DetectTransport("", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Type != TransportTCP {
		t.Error("expected TCP transport via CITECK_HOST env")
	}
	if tc.Host != "staging.co:7088" {
		t.Errorf("expected host staging.co:7088, got %s", tc.Host)
	}
}

func TestDetectTransport_UnixSocket(t *testing.T) {
	// Create a real Unix listener so DetectTransport can dial it
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "") // clear

	tc, err := DetectTransport("", "", "", "", false)
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

func TestDetectTransport_StaleSocket(t *testing.T) {
	// A regular file at the socket path (stale) should not be detected
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")
	if err := os.WriteFile(sockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	_, err := DetectTransport("", "", "", "", false)
	if err == nil {
		t.Error("expected error for stale socket, got nil")
	}
}

// B7-07: a 0-byte regular file at the socket path (left behind after a
// daemon crash) must be reported as a stale socket FILE, not "no socket".
func TestDetectTransport_StaleSocketFile_Message(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")
	if err := os.WriteFile(sockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	_, err := DetectTransport("", "", "", "", false)
	if err == nil {
		t.Fatal("expected error for stale regular file at socket path")
	}
	msg := err.Error()
	if !contains(msg, "stale socket file") {
		t.Errorf("expected message to mention 'stale socket file', got: %q", msg)
	}
	if contains(msg, "no socket at") {
		t.Errorf("should not report 'no socket at' when file exists: %q", msg)
	}
}

func TestDetectTransport_NoDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	_, err := DetectTransport("", "", "", "", false)
	if err == nil {
		t.Error("expected error when no daemon is running")
	}
}

// B7-07: when the socket path genuinely does not exist, the error must
// still say "no socket at" (not the stale-file phrasing).
func TestDetectTransport_NoDaemon_Message(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CITECK_RUN", dir)
	t.Setenv("CITECK_HOST", "")

	_, err := DetectTransport("", "", "", "", false)
	if err == nil {
		t.Fatal("expected error when no daemon is running")
	}
	msg := err.Error()
	if !contains(msg, "no socket at") {
		t.Errorf("expected message to mention 'no socket at' when path is absent, got: %q", msg)
	}
	if contains(msg, "stale") {
		t.Errorf("missing file must not be reported as stale: %q", msg)
	}
}

// contains is a tiny substring helper used by the message-shape tests
// above so we avoid a strings import in a file that does not otherwise
// need one.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestDetectTransport_HostFlagOverridesEnv(t *testing.T) {
	t.Setenv("CITECK_HOST", "env-host:7088")

	tc, err := DetectTransport("flag-host:7088", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if tc.Host != "flag-host:7088" {
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
	tc := &TransportConfig{Type: TransportTCP, Host: "prod.co:7088"}
	if tc.BaseURL() != "http://prod.co:7088" {
		t.Errorf("expected http://prod.co:7088, got %s", tc.BaseURL())
	}
}

func TestBaseURL_TCP_TLS(t *testing.T) {
	tc := &TransportConfig{Type: TransportTCP, Host: "prod.co:7088", TLSCert: "/tmp/cert.pem"}
	if tc.BaseURL() != "https://prod.co:7088" {
		t.Errorf("expected https://prod.co:7088, got %s", tc.BaseURL())
	}
}

func TestBaseURL_TCP_Insecure(t *testing.T) {
	tc := &TransportConfig{Type: TransportTCP, Host: "prod.co:7088", Insecure: true}
	if tc.BaseURL() != "https://prod.co:7088" {
		t.Errorf("expected https://prod.co:7088, got %s", tc.BaseURL())
	}
}
