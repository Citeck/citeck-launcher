package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// writeContextMeta creates a docker context store entry for name pointing at
// host, mirroring the on-disk layout the docker CLI produces.
func writeContextMeta(t *testing.T, home, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	dir := filepath.Join(home, ".docker", "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newUnixSocket binds a real unix socket so the unix-endpoint existence check
// in dockerHostFromContext passes.
func newUnixSocket(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func TestDockerHostFromContext_DesktopLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "")

	sock := filepath.Join(home, ".docker", "run", "docker.sock")
	newUnixSocket(t, sock)
	host := "unix://" + sock
	writeContextMeta(t, home, "desktop-linux", host)

	cfg := `{"currentContext":"desktop-linux"}`
	if err := os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := dockerHostFromContext(); got != host {
		t.Fatalf("dockerHostFromContext() = %q, want %q", got, host)
	}
}

func TestDockerHostFromContext_EnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "colima")

	sock := filepath.Join(home, ".colima", "default", "docker.sock")
	newUnixSocket(t, sock)
	host := "unix://" + sock
	writeContextMeta(t, home, "colima", host)
	// No config.json on purpose: $DOCKER_CONTEXT must win without it.

	if got := dockerHostFromContext(); got != host {
		t.Fatalf("dockerHostFromContext() = %q, want %q", got, host)
	}
}

func TestDockerHostFromContext_TCPReturnedAsIs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "remote")

	host := "tcp://10.0.0.5:2376"
	writeContextMeta(t, home, "remote", host)

	if got := dockerHostFromContext(); got != host {
		t.Fatalf("dockerHostFromContext() = %q, want %q", got, host)
	}
}

func TestDockerHostFromContext_MissingSocketFallsThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "desktop-linux")

	// Context points at a unix socket that does not exist → return "" so the
	// caller falls back to detectDockerSocket.
	writeContextMeta(t, home, "desktop-linux", "unix://"+filepath.Join(home, "nope", "docker.sock"))

	if got := dockerHostFromContext(); got != "" {
		t.Fatalf("dockerHostFromContext() = %q, want empty", got)
	}
}

func TestDockerHostFromContext_NoContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "")
	// No config.json at all → no current context.

	if got := dockerHostFromContext(); got != "" {
		t.Fatalf("dockerHostFromContext() = %q, want empty", got)
	}
}

func TestDockerHostFromContext_DefaultContextIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONTEXT", "")

	if err := os.MkdirAll(filepath.Join(home, ".docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"currentContext":"default"}`
	if err := os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// "default" means "use DOCKER_HOST / built-in default", not a stored context.
	if got := dockerHostFromContext(); got != "" {
		t.Fatalf("dockerHostFromContext() = %q, want empty", got)
	}
}
