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
// `marker` on start so the test can observe which binary ran.
func writeSleepScript(t *testing.T, dir, name, marker string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\necho " + name + " >> " + marker + "\nsleep 30\n"
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

	data, _ := os.ReadFile(marker)
	s := string(data)
	if !strings.Contains(s, "daemonA") || !strings.Contains(s, "daemonB") {
		t.Fatalf("marker = %q, want both daemonA and daemonB", s)
	}
}
