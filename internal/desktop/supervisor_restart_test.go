package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeSleepScript creates an executable shell script that sleeps, so the
// supervisor has a real long-lived child to manage. It appends its own name to
// `marker` on start so the test can observe which binary ran. The shell then
// execs sleep so stopping the supervised child also closes its output pipes;
// a separate sleep process would outlive the shell and hold go test output open.
func writeSleepScript(t *testing.T, dir, name, marker string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\necho " + name + " >> " + marker + "\nexec sleep 30\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSupervisorRestartReSelectsBinary(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	binA := writeSleepScript(t, dir, "daemonA", marker)
	binB := writeSleepScript(t, dir, "daemonB", marker)

	var current atomic.Value
	current.Store(binA)
	var ready atomic.Bool
	ready.Store(true) // pretend the child is instantly ready

	sv := &Supervisor{
		BinarySelector: func() (string, error) { return current.Load().(string), nil },
		Stdin:          "\n",
		ReadyCheck:     ready.Load,
		LogWriter:      os.Stderr,
	}
	ctx := t.Context()
	if err := sv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sv.Wait(2 * time.Second) })

	// Flip the selector to binB and Restart → the new child must be binB.
	// postDaemonShutdown will fail (no daemon socket), so Restart force-kills the
	// child after the grace; superviseLoop then respawns binB.
	current.Store(binB)
	if err := sv.Restart(ctx, 5*time.Second); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// Restart returns once the respawned child is READY — which this test fakes
	// as instant — not once that child has RUN, so the new sh may not have
	// appended its name yet. Reading the marker once raced that echo and lost
	// often enough on macOS to fail outright; wait for it instead. A respawn of
	// the wrong binary still fails: daemonB never appears.
	deadline := time.Now().Add(5 * time.Second)
	var s string
	for {
		data, _ := os.ReadFile(marker)
		s = string(data)
		if strings.Contains(s, "daemonA") && strings.Contains(s, "daemonB") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker = %q, want both daemonA and daemonB", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
