package namespace

import (
	"context"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
)

func TestGracefulShutdownOrder(t *testing.T) {
	apps := []*AppRuntime{
		{Name: appdef.AppPostgres, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppGateway, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
		{Name: appdef.AppProxy, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
		{Name: appdef.AppKeycloak, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppZookeeper, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppEmodel, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
	}

	ordered := gracefulShutdownOrder(apps)

	if len(ordered) != 6 {
		t.Fatalf("expected 6 apps, got %d", len(ordered))
	}

	// Proxy should be first
	if ordered[0].Name != appdef.AppProxy {
		t.Errorf("expected proxy first, got %s", ordered[0].Name)
	}

	// Infrastructure (postgres, zookeeper) should be last
	last2 := ordered[len(ordered)-2:]
	infraNames := map[string]bool{appdef.AppPostgres: true, appdef.AppZookeeper: true}
	for _, app := range last2 {
		if !infraNames[app.Name] {
			t.Errorf("expected infrastructure last, got %s", app.Name)
		}
	}

	// Keycloak should be before infrastructure
	kkIdx := -1
	pgIdx := -1
	for i, app := range ordered {
		if app.Name == appdef.AppKeycloak {
			kkIdx = i
		}
		if app.Name == appdef.AppPostgres {
			pgIdx = i
		}
	}
	if kkIdx >= pgIdx {
		t.Errorf("keycloak (idx %d) should be before postgres (idx %d)", kkIdx, pgIdx)
	}
}

func TestDefaultReconcilerConfig(t *testing.T) {
	cfg := DefaultReconcilerConfig()
	if !cfg.Enabled {
		t.Error("reconciler should be enabled by default")
	}
	if cfg.IntervalSeconds != 60 {
		t.Errorf("expected 60s interval, got %d", cfg.IntervalSeconds)
	}
	if !cfg.LivenessEnabled {
		t.Error("liveness should be enabled by default")
	}
}

func TestCheckLivenessFailureCounting(t *testing.T) {
	md := newMockDocker()
	cfg := testConfig()
	r := NewRuntime(cfg, md, t.TempDir())
	defer r.Shutdown()

	// Set up a running app with HTTP liveness probe (will fail — no HTTP server at mock IP)
	r.mu.Lock()
	r.status = NsStatusRunning
	r.apps["emodel"] = &AppRuntime{
		Name:        "emodel",
		Status:      AppStatusRunning,
		ContainerID: "container-1",
		Def: appdef.ApplicationDef{
			Name: "emodel",
			Kind: appdef.KindCiteckCore,
			LivenessProbe: &appdef.AppProbeDef{
				HTTP:             &appdef.HTTPProbeDef{Path: "/management/health", Port: 8094},
				FailureThreshold: 3,
				TimeoutSeconds:   1,
			},
		},
	}
	r.mu.Unlock()

	ctx := context.Background()

	// First failure — should NOT restart
	r.livenessCheckOnce(ctx)
	r.mu.RLock()
	assert.Equal(t, AppStatusRunning, r.apps["emodel"].Status)
	assert.Equal(t, 1, r.livenessFailures["emodel"])
	r.mu.RUnlock()

	// Second failure — should NOT restart
	r.livenessCheckOnce(ctx)
	r.mu.RLock()
	assert.Equal(t, AppStatusRunning, r.apps["emodel"].Status)
	assert.Equal(t, 2, r.livenessFailures["emodel"])
	r.mu.RUnlock()

	// Third failure — T17a fires: STOPPING transition + restart_event +
	// stopContainer dispatch. After the dispatch, the worker may complete
	// and route through T21 (desiredNext=READY_TO_PULL) → T2 → T5 → T7 →
	// T15 → RUNNING by the time the test reads — so accept any post-T17a
	// status, prioritizing the race-free invariants (counter reset + event).
	r.livenessCheckOnce(ctx)
	r.mu.RLock()
	assert.Contains(t,
		[]AppRuntimeStatus{
			AppStatusStopping, AppStatusReadyToPull, AppStatusPulling,
			AppStatusReadyToStart, AppStatusStarting, AppStatusRunning,
		},
		r.apps["emodel"].Status,
		"T17a should have driven the app through STOPPING (post-T17a status acceptable)")
	assert.Equal(t, 0, r.livenessFailures["emodel"])
	assert.Equal(t, 1, r.apps["emodel"].RestartCount)
	assert.Len(t, r.restartEvents, 1)
	assert.Equal(t, "liveness", r.restartEvents[0].Reason)
	r.mu.RUnlock()
}

func TestCheckLivenessRunsInStalledState(t *testing.T) {
	md := newMockDocker()
	cfg := testConfig()
	r := NewRuntime(cfg, md, t.TempDir())
	defer r.Shutdown()

	r.mu.Lock()
	r.status = NsStatusStalled // Not RUNNING!
	r.apps["emodel"] = &AppRuntime{
		Name:        "emodel",
		Status:      AppStatusRunning,
		ContainerID: "container-1",
		Def: appdef.ApplicationDef{
			Name: "emodel",
			Kind: appdef.KindCiteckCore,
			LivenessProbe: &appdef.AppProbeDef{
				HTTP:             &appdef.HTTPProbeDef{Path: "/management/health", Port: 8094},
				FailureThreshold: 1, // Restart on first failure
				TimeoutSeconds:   1,
			},
		},
	}
	r.mu.Unlock()

	r.livenessCheckOnce(context.Background())

	r.mu.RLock()
	// After T17a fires, the state machine may advance the app through
	// STOPPING → READY_TO_PULL → … by the time the test reads. Accept any
	// post-T17a status; the race-free invariant is the RestartCount bump.
	assert.Contains(t,
		[]AppRuntimeStatus{
			AppStatusStopping, AppStatusReadyToPull, AppStatusPulling,
			AppStatusReadyToStart, AppStatusStarting, AppStatusRunning,
		},
		r.apps["emodel"].Status,
		"liveness should run in STALLED state and trigger a restart via T17a")
	assert.Equal(t, 1, r.apps["emodel"].RestartCount, "restart should have been recorded")
	r.mu.RUnlock()
}

func TestCheckLivenessResetsOnSuccess(t *testing.T) {
	md := newMockDocker()
	cfg := testConfig()
	r := NewRuntime(cfg, md, t.TempDir())
	defer r.Shutdown()

	r.mu.Lock()
	r.status = NsStatusRunning
	r.apps["postgres"] = &AppRuntime{
		Name:        "postgres",
		Status:      AppStatusRunning,
		ContainerID: "container-1",
		Def: appdef.ApplicationDef{
			Name: "postgres",
			Kind: appdef.KindThirdParty,
			LivenessProbe: &appdef.AppProbeDef{
				Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready", "-U", "postgres"}},
				FailureThreshold: 3,
				TimeoutSeconds:   1,
			},
		},
	}
	r.livenessFailures["postgres"] = 2 // Pre-set 2 failures
	r.mu.Unlock()

	// Exec probe succeeds (mockDocker returns exit 0) — should reset counter
	r.livenessCheckOnce(context.Background())

	r.mu.RLock()
	assert.Equal(t, AppStatusRunning, r.apps["postgres"].Status)
	assert.Equal(t, 0, r.livenessFailures["postgres"])
	r.mu.RUnlock()
}
