package namespace

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// failingExecMockDocker wraps mockDocker and forces ExecInContainer to return
// a non-zero exit code. This simulates a flaky liveness probe on a reused
// container — the probe must NOT cause recreation (see regression test below).
type failingExecMockDocker struct {
	*mockDocker
}

func (m *failingExecMockDocker) ExecInContainer(_ context.Context, _ string, _ []string) (output string, exitCode int, err error) {
	return "", 1, nil
}

// TestDoStart_ReusedContainerNotLivenessProbed is a regression test for B6-06:
// doStart must NOT run a synchronous single-shot liveness probe on reused
// containers. Under reload stress that probe flaked transiently, wrongly
// marking the container non-reusable and triggering recreation — cycling the
// app through START_FAILED for ~90s until the reconciler healed it.
//
// Reused containers are validated only via Docker inspect
// (State.Status == "running"). Truly-hung containers are caught by the
// reconciler's threshold-based liveness loop.
//
// This test asserts that when a reused container would fail a liveness probe,
// doStart no longer recreates it.
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
	r1.Start(apps, false)
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

	r2.Start(apps, false)
	if !waitForStatus(r2, NsStatusRunning, 10*time.Second) {
		t.Fatalf("second runtime did not reach RUNNING, got %v", r2.Status())
	}

	md.mu.Lock()
	createsAfter := md.nextID
	containersAfter := len(md.containers)
	md.mu.Unlock()

	if createsAfter != createsBefore {
		t.Fatalf("container was recreated despite hash match — liveness probe must not cause recreation: createsBefore=%d createsAfter=%d", createsBefore, createsAfter)
	}
	if containersAfter != 1 {
		t.Fatalf("expected 1 container after adopt, got %d", containersAfter)
	}
}

// TestDoStart_RefreshImagesGatesSnapshotDigestRefresh and
// TestDoRegenerate_RefreshImagesGatesSnapshotDigestRefresh verify that
// doStart/doRegenerate invoke refreshSnapshotDigests (the :snapshot pre-pull
// digest refresh) iff refreshImages is set. Every reload/config-edit path
// (refreshImages=false) must NOT pay this cost — it caused a hidden re-pull of
// unrelated apps and a dead window before the hash diff. Only the explicit
// Update & Start action passes refreshImages=true.
//
// Seam: refreshSnapshotDigestsFn defaults to the real method (wired in
// NewRuntime) and is overridden here with a call-counting stub, mirroring the
// existing nowFunc/WithTestClock test-injection pattern — this avoids
// depending on real Docker pull semantics (which mockDocker's
// PullImageWithProgress also serves from the state-machine's own T2/T3 pull,
// making a raw pull-call-count assertion ambiguous).
func TestDoStart_RefreshImagesGatesSnapshotDigestRefresh(t *testing.T) {
	for _, tc := range []struct {
		name          string
		refreshImages bool
		wantCalls     int32
	}{
		{"refreshImages=true calls refresh once", true, 1},
		{"refreshImages=false skips refresh", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := newMockDocker()
			tmpDir := t.TempDir()
			app := simpleApp("gateway", "citeck/gateway:snapshot")
			apps := []appdef.ApplicationDef{app}

			r := NewRuntime(testConfig(), md, tmpDir)
			defer r.Shutdown()
			var calls atomic.Int32
			r.refreshSnapshotDigestsFn = func(_ context.Context, _ []appdef.ApplicationDef) {
				calls.Add(1)
			}

			r.Start(apps, tc.refreshImages)
			if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
				t.Fatalf("runtime did not reach RUNNING, got %v", r.Status())
			}

			if got := calls.Load(); got != tc.wantCalls {
				t.Fatalf("refreshSnapshotDigestsFn call count = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestDoRegenerate_RefreshImagesGatesSnapshotDigestRefresh mirrors
// TestDoStart_RefreshImagesGatesSnapshotDigestRefresh for the doRegenerate
// path (config-edit / Update & Start on an already-running namespace).
func TestDoRegenerate_RefreshImagesGatesSnapshotDigestRefresh(t *testing.T) {
	for _, tc := range []struct {
		name          string
		refreshImages bool
		wantCalls     int32
	}{
		{"refreshImages=true calls refresh once", true, 1},
		{"refreshImages=false skips refresh", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := newMockDocker()
			tmpDir := t.TempDir()
			app := simpleApp("gateway", "citeck/gateway:snapshot")
			apps := []appdef.ApplicationDef{app}

			r := NewRuntime(testConfig(), md, tmpDir)
			defer r.Shutdown()
			r.Start(apps, false)
			if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
				t.Fatalf("runtime did not reach RUNNING, got %v", r.Status())
			}

			var calls atomic.Int32
			r.refreshSnapshotDigestsFn = func(_ context.Context, _ []appdef.ApplicationDef) {
				calls.Add(1)
			}

			r.Regenerate(apps, nil, nil, tc.refreshImages)
			// Synchronize on the same STARTING→RUNNING round trip the other
			// regenerate tests use (see TestRegeneratePreservesRunning /
			// TestRegenerateRestartsChanged): doRegenerate flips NS status to
			// STARTING synchronously, and refreshSnapshotDigestsFn is called
			// (or gated away) strictly BEFORE that flip, so observing the
			// return to RUNNING proves doRegenerate ran to completion — and
			// in particular that the refreshImages gate was already
			// evaluated — before we read calls.Load() below. Without this,
			// a wantCalls=0 assertion would trivially pass even if the gate
			// were removed, since the counter starts at 0.
			waitForStatus(r, NsStatusStarting, 5*time.Second)
			if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
				t.Fatalf("namespace did not return to RUNNING after regenerate, got %v", r.Status())
			}

			if got := calls.Load(); got != tc.wantCalls {
				t.Fatalf("refreshSnapshotDigestsFn call count = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}
