//go:build !windows

// This test uses unix signals (syscall.Kill) for the stub child and liveness
// probe; the stub build also skips on Windows. Tagged !windows so Windows
// go vet / go test do not try to compile the unix-only syscall.Kill reference.

package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// buildStubDaemon compiles a tiny program that writes a ready file then sleeps,
// simulating a daemon child. Returns the binary path. The whole test file is
// !windows-tagged (it uses unix signals), so the original windows skip is
// unnecessary here.
func buildStubDaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main
import ("os";"time")
func main(){ _ = os.WriteFile(os.Getenv("READY_FILE"),[]byte("ok"),0o644); time.Sleep(30*time.Second) }`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "stub")
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	return bin
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func TestSupervisorStartsAndStopsChild(t *testing.T) {
	bin := buildStubDaemon(t)
	readyFile := filepath.Join(t.TempDir(), "ready")
	sv := &Supervisor{
		BinaryPath: bin,
		Args:       []string{},
		ExtraEnv:   []string{"READY_FILE=" + readyFile},
		ReadyCheck: func() bool { _, err := os.Stat(readyFile); return err == nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := sv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !sv.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("child never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	pid := sv.Pid()
	if pid <= 0 {
		t.Fatalf("bad pid %d", pid)
	}
	cancel()
	sv.Wait(2 * time.Second)
	if processAlive(pid) {
		t.Fatalf("child %d still alive after stop", pid)
	}
}
