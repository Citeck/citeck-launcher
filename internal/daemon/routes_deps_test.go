package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// newDepsRoutesDaemon builds a daemon whose active namespace carries a REAL
// runtime holding dependency pins, plus the generation verdict the loader
// records (dependencies + held-back upgrades) — the two inputs the dependency
// list derives every status from.
func newDepsRoutesDaemon(t *testing.T) (*Daemon, *http.ServeMux, *namespace.Runtime) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres:  {Image: "postgres:17.5"},
		deps.RabbitMQ:  {Image: "rabbitmq:4.1.2-management"},
		deps.Zookeeper: {Image: "zookeeper:3.9.5"},
	}, nil, nil)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:     rt,
		nsConfig:    &namespace.Config{ID: "ns1"},
		volumesBase: t.TempDir(),
		dependencyUpgrades: []namespace.DependencyUpgrade{
			{ID: deps.Postgres, App: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true},
			{ID: deps.RabbitMQ, App: "rabbitmq", From: "rabbitmq:4.1.2-management", To: "rabbitmq:4.2.9-management"},
		},
		dependencies: map[deps.ID]namespace.DependencyGen{
			deps.Postgres:  {Effective: "postgres:17.5", Candidate: "postgres:18"},
			deps.RabbitMQ:  {Effective: "rabbitmq:4.1.2-management", Candidate: "rabbitmq:4.2.9-management"},
			deps.Zookeeper: {Effective: "zookeeper:3.9.7", Candidate: "zookeeper:3.9.7"},
			deps.Keycloak:  {Effective: "keycloak/keycloak:26.4.5", Candidate: "keycloak/keycloak:26.4.5"},
		},
	}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, rt
}

func depsGet(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rec
}

func depsPost(mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeDependencies(t *testing.T, rec *httptest.ResponseRecorder) api.DependenciesDto {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var dto api.DependenciesDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	return dto
}

func TestListDependenciesDerivesStatuses(t *testing.T) {
	_, mux, _ := newDepsRoutesDaemon(t)
	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	byID := map[string]api.DependencyDto{}
	for _, it := range dto.Items {
		byID[it.ID] = it
	}
	assert.Equal(t, api.DependencyUpgradeAvailable, byID["postgres"].Status)
	assert.Equal(t, "17.5", byID["postgres"].CurrentVersion)
	assert.Equal(t, "18", byID["postgres"].TargetVersion)
	assert.True(t, byID["postgres"].Migratable)
	assert.Equal(t, "postgres", byID["postgres"].App)
	assert.Equal(t, api.DependencyRequiresLauncherUpdate, byID["rabbitmq"].Status)
	assert.False(t, byID["rabbitmq"].Migratable)
	assert.Equal(t, api.DependencyPendingMinor, byID["zookeeper"].Status, "pinned 3.9.5, generator emits 3.9.7")
	assert.Equal(t, api.DependencyUpToDate, byID["keycloak"].Status, "no pin, effective == candidate")
	assert.NotContains(t, byID, "mongodb", "a dependency this namespace does not run is not listed")
	// registry order
	require.Len(t, dto.Items, 4)
	assert.Equal(t, "postgres", dto.Items[0].ID)
	assert.Equal(t, "rabbitmq", dto.Items[1].ID)
	assert.Nil(t, dto.Migration)
	assert.Empty(t, dto.RollbackPending)
}

// The target VERSION must be read off the image the user is actually offered
// (the held-back target), not off the generation's candidate: they are the
// same today only because both come from one generation, and a list that
// showed "18" beside an image of postgres:19 would be a lie about what the
// migrate button does.
func TestDependencyTargetVersionFollowsTheHeldBackTarget(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dependencyUpgrades[0].To = "postgres:19"
	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	assert.Equal(t, "postgres:19", dto.Items[0].TargetImage)
	assert.Equal(t, "19", dto.Items[0].TargetVersion)
}

// An OPEN journal on a namespace nobody is migrating means the last rollback
// failed. The list has to say so: while it stands, the runtime's RUNNING
// re-pin hook is paused and the dependency's pin cannot move at all.
func TestListDependenciesReportsAPendingRollback(t *testing.T) {
	_, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", Step: "restore"},
		&deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
			FinishedAt: time.Now(), Error: "rollback failed: remove volume postgres3: boom"})

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	assert.Contains(t, dto.RollbackPending, "rollback")
	assert.Contains(t, dto.RollbackPending, "postgres")
	assert.Contains(t, dto.RollbackPending, "boom", "the reason the rollback failed is the actionable part")
	require.NotNil(t, dto.LastResult)
	assert.False(t, dto.LastResult.Success)
	assert.Equal(t, "postgres", dto.LastResult.ID)
}

func TestListDependenciesRefusesWithoutANamespace(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	rec := depsGet(mux, api.Dependencies)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeNotConfigured)
}

func TestNamespaceDtoCarriesUpgradesAndScopedMigration(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", Step: "dump", StepIndex: 4, StepCount: 10})
	rec := depsGet(mux, api.Namespace)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var ns api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ns))
	require.Len(t, ns.DependencyUpgrades, 2)
	assert.Equal(t, "postgres", ns.DependencyUpgrades[0].ID)
	assert.Equal(t, "postgres:18", ns.DependencyUpgrades[0].To)
	assert.True(t, ns.DependencyUpgrades[0].Migratable)
	require.NotNil(t, ns.DependencyMigration)
	assert.Equal(t, "dump", ns.DependencyMigration.Step)

	d.setDepsMigration("other-ns", &api.DependencyMigrationDto{ID: "postgres", Step: "dump"})
	var other api.NamespaceDto
	require.NoError(t, json.Unmarshal(depsGet(mux, api.Namespace).Body.Bytes(), &other))
	assert.Nil(t, other.DependencyMigration, "another namespace's migration is not shown here")

	d.setDepsMigration("ns1", nil)
	var cleared api.NamespaceDto
	require.NoError(t, json.Unmarshal(depsGet(mux, api.Namespace).Body.Bytes(), &cleared))
	assert.Nil(t, cleared.DependencyMigration)
}

func TestMigrateRefusals(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)

	rec := depsPost(mux, api.DependencyMigratePath("rabbitmq"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)

	rec = depsPost(mux, api.DependencyMigratePath("keycloak"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyUpToDate)

	rec = depsPost(mux, api.DependencyMigratePath("nope"), "{}")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyUnknown)

	rec = depsPost(mux, api.DependencyMigratePath("postgres"), "{not json")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	require.True(t, d.longOp.TryLock(longOpSnapshot))
	rec = depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	d.longOp.Unlock()
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
	assert.Contains(t, rec.Body.String(), "snapshot", "the refusal names the real holder")

	// no docker client → 503, and the long-operation lock is handed back
	rec = depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, longOpNone, d.longOp.Holder(), "a refused migration must not keep the lock")
}

// The migration's first step STOPS the namespace, and Runtime.Stop only
// ENQUEUES the command: a runtime that is mid-transition may never read it
// (the same trap as the Update & Start queue's STOPPING arm), so the whole
// migration would burn its stop timeout and fail. Only a namespace that is
// plainly RUNNING or plainly STOPPED may be migrated.
func TestMigrateRefusesANamespaceMidTransition(t *testing.T) {
	for _, st := range []namespace.NsRuntimeStatus{
		namespace.NsStatusStarting, namespace.NsStatusStopping, namespace.NsStatusStalled,
	} {
		t.Run(string(st), func(t *testing.T) {
			d, mux, rt := newDepsRoutesDaemon(t)
			rt.SetStatusForTest(st)
			rec := depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
			assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNamespaceBusy)
			assert.Equal(t, longOpNone, d.longOp.Holder())
		})
	}
	for _, st := range []namespace.NsRuntimeStatus{namespace.NsStatusRunning, namespace.NsStatusStopped} {
		t.Run(string(st), func(t *testing.T) {
			_, mux, rt := newDepsRoutesDaemon(t)
			rt.SetStatusForTest(st)
			rec := depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
				"past the status gate; only the missing docker client stops it")
		})
	}
}

// An open journal with nobody migrating is an interrupted migration whose
// rollback has not succeeded yet. Starting a second migration over those
// leftovers is exactly what the journal exists to prevent.
func TestMigrateRefusesWhileAJournalIsOpen(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"},
		&deps.MigrationResult{ID: deps.Postgres, Error: "rollback failed: boom"})

	rec := depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress)
	assert.Contains(t, rec.Body.String(), "rollback")
	assert.Contains(t, rec.Body.String(), "boom")
	assert.Equal(t, longOpNone, d.longOp.Holder())

	// While a migration really is running the wording is the plain one — there
	// is nothing pending to retry, something is happening right now.
	d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", Step: "dump"})
	rec = depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress)
	assert.NotContains(t, rec.Body.String(), "rollback")
}

func TestPreflightRouteRefusesUnknownAndNotMigratable(t *testing.T) {
	_, mux, _ := newDepsRoutesDaemon(t)
	rec := depsGet(mux, api.DependencyPreflightPath("nope"))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyUnknown)

	rec = depsGet(mux, api.DependencyPreflightPath("rabbitmq"))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)

	rec = depsGet(mux, api.DependencyPreflightPath("postgres"))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no docker client to probe with")
}

// The preflight is the confirm dialog's input, so the one condition that will
// refuse the migration must appear there as a problem rather than as a green
// dialog followed by a 409.
func TestPreflightReportsAPendingRollbackAsAProblem(t *testing.T) {
	_, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"},
		&deps.MigrationResult{ID: deps.Postgres, Error: "rollback failed: boom"})

	rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var pre migrate.PreflightResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pre))
	assert.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)
	assert.Contains(t, pre.Problems[0], "rollback")
	assert.Equal(t, "postgres:17.5", pre.From)
	assert.Equal(t, "postgres:18", pre.To)
}

type fakeMigrator struct {
	pre    migrate.PreflightResult
	plan   *migrate.Plan
	err    error
	onPlan func(from, to string, opts migrate.PlanOptions)
}

func (f fakeMigrator) Preflight(context.Context, migrate.Env, string, string) migrate.PreflightResult {
	return f.pre
}

func (f fakeMigrator) Plan(_ context.Context, _ migrate.Env, from, to string, opts migrate.PlanOptions) (*migrate.Plan, deps.MigrationJournal, error) {
	if f.onPlan != nil {
		f.onPlan(from, to, opts)
	}
	if f.err != nil {
		return nil, deps.MigrationJournal{}, f.err
	}
	return f.plan, deps.MigrationJournal{ID: deps.Postgres, From: from, To: to}, nil
}

func TestMigrateAcceptsRunsAndBroadcasts(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)

	var holderDuringStep longOpKind
	var progressSeen *api.DependencyMigrationDto
	var planArgs struct {
		from, to string
		opts     migrate.PlanOptions
	}
	finalized := make(chan struct{})
	d.depsMigratorFn = func(migrate.Env) depsMigrator {
		return fakeMigrator{
			pre: migrate.PreflightResult{OK: true, From: "postgres:17.5", To: "postgres:18"},
			onPlan: func(from, to string, opts migrate.PlanOptions) {
				planArgs.from, planArgs.to, planArgs.opts = from, to, opts
			},
			plan: &migrate.Plan{
				Steps: []migrate.Step{{ID: "only", Run: func(_ context.Context, _ *migrate.Journal, p migrate.StepProgress) error {
					holderDuringStep = d.longOp.Holder()
					p(50, "half")
					progressSeen = d.currentDepsMigration("ns1")
					return nil
				}}},
				Rollback: func(context.Context, *deps.MigrationJournal) error { return nil },
				Result: func(j *deps.MigrationJournal) deps.MigrationResult {
					return deps.MigrationResult{ID: j.ID, OldVolume: "postgres2"}
				},
				Finalize: func(context.Context, *deps.MigrationJournal) error { close(finalized); return nil },
			},
		}
	}
	events, _, ok := d.addSubscriber()
	require.True(t, ok)
	defer d.removeSubscriber(events)

	rec := depsPost(mux, api.DependencyMigratePath("postgres"), `{"replaceExistingVolume":true}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	select {
	case <-finalized:
	case <-time.After(10 * time.Second):
		t.Fatal("the migration never reached its finalize step")
	}
	d.bgWg.Wait()

	assert.Equal(t, "postgres:18", rt.DependencyPins()[deps.Postgres], "the commit moved the pin")
	assert.Nil(t, rt.MigrationJournal(), "a committed migration closes its journal")
	require.NotNil(t, rt.LastDependencyMigration())
	assert.Equal(t, "postgres2", rt.LastDependencyMigration().OldVolume)

	assert.Equal(t, "postgres:17.5", planArgs.from)
	assert.Equal(t, "postgres:18", planArgs.to)
	assert.True(t, planArgs.opts.ReplaceExistingVolume, "the request body's confirmation reaches the plan")
	assert.Equal(t, longOpNone, d.longOp.Holder(), "the lock is released when the goroutine ends")
	assert.Equal(t, longOpMigration, holderDuringStep,
		"the lock must be held AS a migration — labeled a plain request it would let Start/Stop run beside the volume rewrite")
	require.NotNil(t, progressSeen)
	assert.Equal(t, "only", progressSeen.Step)
	assert.InDelta(t, 50.0, progressSeen.Percent, 0.001)

	byType := map[string]api.EventDto{}
	for len(events) > 0 {
		e := <-events
		byType[e.Type] = e
	}
	require.Contains(t, byType, "deps_migration_start")
	require.Contains(t, byType, "deps_migration_progress")
	require.Contains(t, byType, "deps_migration_complete")
	assert.NotContains(t, byType, "deps_migration_error")
	prog := byType["deps_migration_progress"]
	assert.Equal(t, "postgres", prog.AppName, "AppName carries the dependency id")
	assert.Equal(t, "only", prog.Phase)
	assert.Equal(t, 1, prog.Current)
	assert.Equal(t, 1, prog.Total)
	assert.Equal(t, "ns1", prog.NamespaceID)
	assert.InDelta(t, 50.0, prog.Percent, 0.001)
	assert.Equal(t, "half", prog.After)

	assert.Nil(t, d.currentDepsMigration("ns1"), "progress state is cleared when the pass ends")
}

func TestMigrateReportsAFailedRunAsAnErrorEvent(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)

	rolledBack := make(chan struct{})
	d.depsMigratorFn = func(migrate.Env) depsMigrator {
		return fakeMigrator{plan: &migrate.Plan{
			Steps: []migrate.Step{{ID: "only", Run: func(context.Context, *migrate.Journal, migrate.StepProgress) error {
				return assert.AnError
			}}},
			Rollback: func(context.Context, *deps.MigrationJournal) error { close(rolledBack); return nil },
			Result:   func(*deps.MigrationJournal) deps.MigrationResult { return deps.MigrationResult{} },
		}}
	}
	events, _, ok := d.addSubscriber()
	require.True(t, ok)
	defer d.removeSubscriber(events)

	require.Equal(t, http.StatusAccepted, depsPost(mux, api.DependencyMigratePath("postgres"), "{}").Code)
	select {
	case <-rolledBack:
	case <-time.After(10 * time.Second):
		t.Fatal("the failing step never rolled back")
	}
	d.bgWg.Wait()

	assert.Equal(t, "postgres:17.5", rt.DependencyPins()[deps.Postgres], "a failed migration never moves the pin")
	var types []string
	for len(events) > 0 {
		types = append(types, (<-events).Type)
	}
	assert.Contains(t, types, "deps_migration_error")
	assert.NotContains(t, types, "deps_migration_complete")
	assert.Equal(t, longOpNone, d.longOp.Holder())
	assert.Nil(t, d.currentDepsMigration("ns1"))
}

// A plan that cannot be built (the preflight inside it refused) is a
// SYNCHRONOUS refusal: nothing was started, so it must answer on the request
// rather than 202-and-an-error-event, and it must hand the lock back.
func TestMigrateReportsAPlanRefusalSynchronously(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.depsMigratorFn = func(migrate.Env) depsMigrator {
		return fakeMigrator{err: assert.AnError}
	}
	rec := depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyPreflightFailed)
	assert.Equal(t, longOpNone, d.longOp.Holder())
}

// The preflight probes volumes and containers, so while a migration is
// rewriting them it must answer from the journal instead of measuring a world
// in flux — and say which of the two journal states it found.
func TestPreflightDuringARunningMigrationDoesNotProbe(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"}, nil)
	d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", Step: "dump"})
	d.activeNs.dockerClient = &docker.Client{}
	d.depsMigratorFn = func(migrate.Env) depsMigrator {
		t.Error("the preflight must not probe while a migration is running")
		return fakeMigrator{}
	}

	rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var pre migrate.PreflightResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pre))
	assert.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)
	assert.Contains(t, pre.Problems[0], "already running")
	assert.NotContains(t, pre.Problems[0], "rollback")
}

// A dependency the last generation did NOT emit is not listed, even when a pin
// survives from when it did run: turning mongo off leaves the pin behind (the
// volume is deliberately kept), and a row for it would carry an empty target
// and claim "up-to-date" about a container that no longer exists.
func TestListDependenciesOmitsAPinnedButUngeneratedDependency(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5"},
		deps.MongoDB:  {Image: "mongo:6.0"},
	}, nil, nil)
	require.NotContains(t, d.activeNs.dependencies, deps.MongoDB, "the fixture's generation has no mongo")

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	for _, it := range dto.Items {
		assert.NotEqual(t, string(deps.MongoDB), it.ID, "an ungenerated dependency must not be listed")
		assert.NotEmpty(t, it.TargetImage, "every listed dependency has something to move to")
	}
}
