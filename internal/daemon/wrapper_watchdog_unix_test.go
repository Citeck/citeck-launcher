//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("the current process must report alive")
	}
	if processAlive(-1) || processAlive(0) {
		t.Fatal("non-positive pids must report not alive")
	}

	// A child that has fully exited and been reaped must report not alive.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process failed: %v", err)
	}
	if processAlive(pid) {
		t.Fatalf("reaped process pid=%d must report not alive", pid)
	}
}
