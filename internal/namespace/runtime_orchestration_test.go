package namespace

import (
	"context"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// failingExecMockDocker wraps mockDocker and forces ExecInContainer to return
// a non-zero exit code. This simulates a flaky liveness probe on a reused
// container — under the buggy Phase 2 probe, this would mark the plan
// non-reusable and recreate the container.
type failingExecMockDocker struct {
	*mockDocker
}

func (m *failingExecMockDocker) ExecInContainer(_ context.Context, _ string, _ []string) (output string, exitCode int, err error) {
	return "", 1, nil
}

// TestDoStart_ReusedContainerNotLivenessProbed is a regression test for B6-06.
//
// Before the fix, doStart ran a synchronous single-shot liveness probe on
// reused containers. Under reload stress this probe flaked transiently,
// wrongly marking the container non-reusable and triggering recreation —
// cycling the app through START_FAILED for ~90s until the reconciler healed
// it.
//
// The fix removes that probe entirely. Reused containers are only validated
// via Docker inspect (State.Status == "running"). Truly-hung containers are
// still caught by the reconciler's threshold-based liveness loop.
//
// This test asserts that when a reused container would fail a liveness
// probe, doStart no longer recreates it.
func TestDoStart_ReusedContainerNotLivenessProbed(t *testing.T) {
	md := &failingExecMockDocker{mockDocker: newMockDocker()}
	tmpDir := t.TempDir()

	// An app with an Exec liveness probe that (in the mock) will always fail.
	app := simpleApp("postgres", "postgres:17")
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:           &appdef.ExecProbeDef{Command: []string{"false"}},
		PeriodSeconds:  30,
		TimeoutSeconds: 5,
	}
	apps := []appdef.ApplicationDef{app}

	// First runtime: create the container normally. doStart must not run the
	// (removed) probe on the freshly-created container either.
	r1 := NewRuntime(testConfig(), md, tmpDir)
	r1.Start(apps)
	if !waitForStatus(r1, NsStatusRunning, 10*time.Second) {
		t.Fatalf("first runtime did not reach RUNNING, got %v", r1.Status())
	}

	md.mu.Lock()
	createsBefore := md.nextID
	containersBefore := len(md.containers)
	md.mu.Unlock()
	if containersBefore != 1 {
		t.Fatalf("expected 1 container after first start, got %d", containersBefore)
	}

	// Detach (leave container running) — simulates a daemon restart where the
	// next doStart must adopt the existing container via hash match.
	r1.ShutdownDetached()

	// Second runtime adopts the same mock. The reused container would fail
	// the liveness probe (ExecInContainer returns exit=1), but with the fix
	// the probe is gone and the container must be preserved.
	r2 := NewRuntime(testConfig(), md, tmpDir)
	defer r2.Shutdown()

	r2.Start(apps)
	if !waitForStatus(r2, NsStatusRunning, 10*time.Second) {
		t.Fatalf("second runtime did not reach RUNNING, got %v", r2.Status())
	}

	md.mu.Lock()
	createsAfter := md.nextID
	containersAfter := len(md.containers)
	md.mu.Unlock()

	if createsAfter != createsBefore {
		t.Fatalf("container was recreated despite hash match — Phase 2 liveness probe leaked back in: createsBefore=%d createsAfter=%d", createsBefore, createsAfter)
	}
	if containersAfter != 1 {
		t.Fatalf("expected 1 container after adopt, got %d", containersAfter)
	}
}
