package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/msg"
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
			{ID: deps.Postgres, App: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true,
				Path: []string{"postgres:17.5", "postgres:18"}},
			{ID: deps.RabbitMQ, App: "rabbitmq", From: "rabbitmq:4.1.2-management", To: "rabbitmq:4.2.9-management",
				Path: []string{"rabbitmq:4.1.2-management", "rabbitmq:4.2.9-management"}},
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

// decodeNamespace reads the namespace DTO the dashboard polls.
func decodeNamespace(t *testing.T, rec *httptest.ResponseRecorder) api.NamespaceDto {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	return dto
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
	// RabbitMQ 4.1 → 4.2 is a hop the vendor supports and this launcher ships
	// a plan for, so it is an ordinary offer — it was "requires-launcher-update"
	// only while PostgreSQL was the one migratable dependency.
	assert.Equal(t, api.DependencyUpgradeAvailable, byID["rabbitmq"].Status)
	assert.True(t, byID["rabbitmq"].Migratable)
	assert.Empty(t, byID["rabbitmq"].StatusDetail)
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

// The same pending rollback has to reach a client that never opens the
// dependencies dialog. Load-time recovery (recoverInterruptedMigration) is
// what finds it, and that emits no deps_migration_* event and produces no
// result, so before this the alarm depended on the user going looking for it.
// The ordinary namespace fetch — which every client makes on connect and after
// every reconnect — now carries it.
func TestNamespaceDtoCarriesAPendingRollback(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18", Step: "restore"},
		&deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
			FinishedAt: time.Now(), Error: "rollback failed: remove volume postgres3: boom"})

	dto := decodeNamespace(t, depsGet(mux, "/api/v1/namespace"))
	assert.Contains(t, dto.DependencyRollbackPending, "rollback")
	assert.Contains(t, dto.DependencyRollbackPending, "postgres")
	assert.Contains(t, dto.DependencyRollbackPending, "boom",
		"the reason the rollback failed is the actionable part, exactly as on the dependencies route")
	assert.Equal(t, decodeDependencies(t, depsGet(mux, api.Dependencies)).RollbackPending,
		dto.DependencyRollbackPending, "one condition, one wording — the two surfaces must not drift")

	// While a migration for this namespace IS running, the journal is simply
	// its record: DependencyMigration already describes it, and reporting both
	// would show one condition as two.
	d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", StepCount: 10})
	assert.Empty(t, decodeNamespace(t, depsGet(mux, "/api/v1/namespace")).DependencyRollbackPending)
	d.setDepsMigration("ns1", nil)

	// A namespace with no journal says nothing.
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	assert.Empty(t, decodeNamespace(t, depsGet(mux, "/api/v1/namespace")).DependencyRollbackPending)
}

// The alarm belongs to ONE namespace: the journal it reports lives on that
// namespace's runtime, so switching to another namespace must not carry it
// over — the same rule Updating and UpdateError follow, and for the same
// reason (an alarm shown on a namespace it has nothing to do with).
func TestPendingRollbackIsScopedToItsNamespace(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"}, nil)
	require.NotEmpty(t, decodeNamespace(t, depsGet(mux, "/api/v1/namespace")).DependencyRollbackPending)

	other := namespace.NewRuntime(&namespace.Config{ID: "ns2"}, planStubDocker{}, t.TempDir())
	t.Cleanup(other.Shutdown)
	d.activeNs = &activeNamespace{runtime: other, nsConfig: &namespace.Config{ID: "ns2"}}
	assert.Empty(t, decodeNamespace(t, depsGet(mux, "/api/v1/namespace")).DependencyRollbackPending,
		"the namespace the user switched to has no interrupted migration")
}

// An EMPTY bundle is the launcher's most misleading state, and the dependency
// list is one of the surfaces that could quietly say nothing in it: the infra
// containers are generated unconditionally, with hardcoded fallback images, so
// the namespace really does run postgres/rabbitmq/zookeeper (and mongo, before
// generation 2) even when not one Citeck app was resolved. The list reports
// exactly those — it is about infrastructure versions, and the namespace being
// empty is BundleError's story (a non-dismissible banner) — and an EMPTY list
// would mean something else entirely: that no generation has been recorded for
// this namespace at all.
func TestDependenciesAreListedEvenWithAnEmptyBundle(t *testing.T) {
	cfg := &namespace.Config{ID: "ns1"}
	resp, err := namespace.Generate(cfg, &bundle.EmptyDef, &bundle.WorkspaceConfig{}, namespace.SystemSecrets{})
	require.NoError(t, err)
	for _, app := range resp.Applications {
		require.NotEqual(t, "gateway", app.Name, "this fixture must really be an empty bundle")
	}

	rt := namespace.NewRuntime(cfg, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	d := &Daemon{activeNs: &activeNamespace{
		runtime: rt, nsConfig: cfg, dependencies: resp.Dependencies,
		bundleError: "bundle citeck:community-latest resolved to 0 applications",
	}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	ids := make([]string, 0, len(dto.Items))
	for _, it := range dto.Items {
		ids = append(ids, it.ID)
		assert.NotEmpty(t, it.CurrentImage, "%s is listed, so it must name the image it runs", it.ID)
	}
	assert.Contains(t, ids, "postgres")
	assert.Contains(t, ids, "rabbitmq")
	assert.Contains(t, ids, "zookeeper")
	assert.NotContains(t, ids, "keycloak", "authentication is off in this config, so no keycloak is generated")
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

	// A dependency this launcher ships NO plan for. Keycloak is the one in this
	// fixture (its state lives in the postgres database and there is nothing
	// to migrate on its own), so the upgrade is injected here rather than in
	// the shared fixture, which every other test reads.
	d.activeNs.dependencyUpgrades = append(d.activeNs.dependencyUpgrades, namespace.DependencyUpgrade{
		ID: deps.Keycloak, App: "keycloak", From: "keycloak/keycloak:26.4.5", To: "keycloak/keycloak:27.0.0",
	})
	rec := depsPost(mux, api.DependencyMigratePath("keycloak"), "{}")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)
	d.activeNs.dependencyUpgrades = d.activeNs.dependencyUpgrades[:len(d.activeNs.dependencyUpgrades)-1]

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
	d, mux, _ := newDepsRoutesDaemon(t)
	rec := depsGet(mux, api.DependencyPreflightPath("nope"))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyUnknown)

	// Keycloak is the dependency this launcher ships no plan for.
	d.activeNs.dependencyUpgrades = append(d.activeNs.dependencyUpgrades, namespace.DependencyUpgrade{
		ID: deps.Keycloak, App: "keycloak", From: "keycloak/keycloak:26.4.5", To: "keycloak/keycloak:27.0.0",
	})
	rec = depsGet(mux, api.DependencyPreflightPath("keycloak"))
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
	var pre api.PreflightResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pre))
	assert.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)
	assert.Contains(t, pre.Problems[0], "rollback")
	assert.Equal(t, "postgres:17.5", pre.From)
	assert.Equal(t, "postgres:18", pre.To)
}

// A pair the launcher UNDERSTANDS but has no plan for — a future PostgreSQL
// major whose data layout this release does not know — must say "update the
// launcher" rather than offer a button that ends in a message about volumes.
//
// It is driven through a FAKE migrator rather than through a real version
// pair, and deliberately so. The pair that used to demonstrate it (18 → 19)
// became supported the moment migrations started landing in a fresh volume
// generation, so pinning the behavior to a version would have deleted the
// test along with the limitation — while UnsupportedPairProblem is exactly the
// message the NEXT layout-changing major will need, and the contract that
// keeps it reachable is SupportsPair's, not any particular version's.
func TestAPairTheLauncherCannotCarryIsReportedAsALauncherUpdate(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18"}}, nil, nil)
	d.activeNs.dependencyUpgrades[0] = namespace.DependencyUpgrade{
		ID: deps.Postgres, App: "postgres", From: "postgres:18", To: "postgres:99", Migratable: true,
		Path: []string{"postgres:18", "postgres:99"},
	}
	d.activeNs.dependencies[deps.Postgres] = namespace.DependencyGen{
		Effective: "postgres:18", Candidate: "postgres:99",
	}
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{supports: func(from, to deps.Version) (bool, msg.Message) {
			return false, migrate.UnsupportedPairProblem(from.String(), to.String())
		}}
	}

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	require.NotEmpty(t, dto.Items)
	assert.Equal(t, api.DependencyRequiresLauncherUpdate, dto.Items[0].Status)
	assert.Empty(t, dto.Items[0].StatusDetail,
		"the label is the whole answer here; StatusDetail is for a status a label cannot explain")
	assert.True(t, dto.Items[0].Migratable, "the DEPENDENCY is migratable; this PAIR is not")

	// Both routes refuse it, and the message names the versions — "postgres
	// cannot be migrated" would contradict the 17 → 18 this same launcher does.
	for _, rec := range []*httptest.ResponseRecorder{
		depsGet(mux, api.DependencyPreflightPath("postgres")),
		depsPost(mux, api.DependencyMigratePath("postgres"), "{}"),
	} {
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)
		assert.Contains(t, rec.Body.String(), "update the launcher")
		assert.Contains(t, rec.Body.String(), "99")
	}
}

// The BANNER reads NamespaceDto.dependencyUpgrades, not the dependency list, and
// it headlines the two cases differently — "Dependency upgrade available"
// against "Newer launcher needed for". Built straight from the generator's
// per-DEPENDENCY verdict it advertised an 18 → 19 held back for want of a
// layout as a click away from migrating.
func TestNamespaceDtoMarksAnUnsupportedPairAsNotMigratable(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18"}}, nil, nil)
	d.activeNs.dependencyUpgrades[0] = namespace.DependencyUpgrade{
		ID: deps.Postgres, App: "postgres", From: "postgres:18", To: "postgres:99", Migratable: true,
		Path: []string{"postgres:18", "postgres:99"},
	}
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "postgres"}}
	// Same fake as the list route's test, and for the same reason: the
	// limitation is SupportsPair's answer, not a property of any version pair
	// this release happens not to handle.
	refuse := true
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{supports: func(from, to deps.Version) (bool, msg.Message) {
			if !refuse {
				return true, msg.Message{}
			}
			return false, migrate.UnsupportedPairProblem(from.String(), to.String())
		}}
	}

	rec := depsGet(mux, api.Namespace)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Len(t, dto.DependencyUpgrades, 2)
	assert.Equal(t, "postgres", dto.DependencyUpgrades[0].ID)
	assert.False(t, dto.DependencyUpgrades[0].Migratable,
		"this pair has no plan in this release — the banner must say 'newer launcher'")
	assert.Empty(t, dto.DependencyUpgrades[0].Blocked,
		"the VENDOR did not refuse it; only the launcher did, and the two must not be worded alike")
	// The pair the launcher WAS built for still reaches the banner as an offer.
	refuse = false
	d.activeNs.dependencyUpgrades[0].From, d.activeNs.dependencyUpgrades[0].To = "postgres:17.5", "postgres:18"
	rec = depsGet(mux, api.Namespace)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.DependencyUpgrades[0].Migratable)
}

// A DOWNGRADE is not a missing feature — no launcher will ever grow it — so it
// must NOT be answered with "update the launcher". Supports() rejects it (it
// demands a forward move), which is exactly why it needs its own carve-out:
// without one the preflight's accurate wording was unreachable through both
// routes.
func TestADowngradeGetsItsOwnMessageNotALauncherUpdate(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18"}}, nil, nil)
	d.activeNs.dependencyUpgrades[0] = namespace.DependencyUpgrade{
		ID: deps.Postgres, App: "postgres", From: "postgres:18", To: "postgres:17", Migratable: true,
		Path: []string{"postgres:18", "postgres:17"},
	}
	d.activeNs.dependencies[deps.Postgres] = namespace.DependencyGen{
		Effective: "postgres:18", Candidate: "postgres:17",
	}
	assert.True(t, d.routeProblem(deps.Postgres, migrate.Path{"postgres:18", "postgres:17"}).Empty(),
		"a downgrade is not a launcher problem: the migrator refuses it with an EMPTY reason, "+
			"which is the contract that leaves the preflight's accurate wording reachable")

	// The route lets it through instead of short-circuiting: it gets as far as
	// the Docker probe (503 on this daemon, which has no client), where an
	// unsupported forward pair is refused with 409 NOT_MIGRATABLE first.
	rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "update the launcher")
	rec = depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	assert.NotContains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)

	// And the message it will get is the accurate one, which had become
	// unreachable through both routes.
	env := migratetest.New()
	env.Volumes[postgresVolume(2)] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	pre := migrate.PostgresMigrator{}.Preflight(context.Background(), env, migrate.Path{"postgres:18", "postgres:17"})
	assert.False(t, pre.OK)
	assert.Contains(t, renderProblems(englishForLogs, pre.Problems), "does not migrate data backwards")
}

// A migratable dependency whose held-back upgrade carries an EMPTY route:
// the gate's own no-route case, produced when a rung's tag cannot be parsed
// (see TestAnUnreadableRungLeavesNoRoute and
// TestAMalformedLadderOnAPinnedStandStillHoldsThePinAndIsReported in
// internal/namespace/generator_deps_ladder_test.go — both leave held.Path
// empty for a Migratable() dependency). There is nothing to plan a migration
// FROM, so both routes must refuse it with the same code an unmigratable
// dependency gets, and — this is the part a revert to [upgrade.From,
// upgrade.To] would break silently — the refusal must not name a fabricated
// pair: the operator already has the tag-level detail through the list
// route's StatusDetail.
func TestBothMigrationRoutesRefuseAnUpgradeWithNoRouteAtAll(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dependencyUpgrades[0] = namespace.DependencyUpgrade{
		ID: deps.Postgres, App: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true,
		// Path is deliberately left unset (nil): the gate's EMPTY-route case.
	}

	for _, rec := range []*httptest.ResponseRecorder{
		depsGet(mux, api.DependencyPreflightPath("postgres")),
		depsPost(mux, api.DependencyMigratePath("postgres"), "{}"),
	} {
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)
		assert.NotContains(t, rec.Body.String(), "17.5",
			"no route means nothing to name; a fabricated [From, To] pair would leak the version here")
		assert.NotContains(t, rec.Body.String(), "postgres:18")
	}
}

// The pair the launcher WAS built for stays offered — the guard above must not
// swallow the feature it guards.
func TestTheSupportedPairIsStillOffered(t *testing.T) {
	_, mux, _ := newDepsRoutesDaemon(t)
	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	assert.Equal(t, api.DependencyUpgradeAvailable, dto.Items[0].Status, "17.5 → 18")
}

// Every condition that will 409 the click is the confirm screen's own input.
// Reporting them one 409 at a time, after the user has read a green preflight
// and pressed the button, is the shape this exists to avoid.
func TestPreflightReportsEveryRefusalItCanAnswerWithoutDocker(t *testing.T) {
	t.Run("another long operation", func(t *testing.T) {
		d, mux, _ := newDepsRoutesDaemon(t)
		require.True(t, d.longOp.TryLock(longOpSnapshot))
		t.Cleanup(d.longOp.Unlock)
		pre := decodePreflight(t, depsGet(mux, api.DependencyPreflightPath("postgres")))
		assert.False(t, pre.OK)
		require.Len(t, pre.Problems, 1)
		assert.Contains(t, pre.Problems[0], "a snapshot is in progress")
	})
	t.Run("a namespace mid-transition", func(t *testing.T) {
		_, mux, rt := newDepsRoutesDaemon(t)
		rt.SetStatusForTest(namespace.NsStatusStarting)
		pre := decodePreflight(t, depsGet(mux, api.DependencyPreflightPath("postgres")))
		assert.False(t, pre.OK)
		require.Len(t, pre.Problems, 1)
		assert.Contains(t, pre.Problems[0], "start or stop it")
	})
	// One condition, one line. During a migration the journal and the lock
	// describe the same fact from two angles, and a confirm screen listing it
	// twice reads as two problems.
	t.Run("a running migration is reported once", func(t *testing.T) {
		d, mux, rt := newDepsRoutesDaemon(t)
		rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
			&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"}, nil)
		d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", Step: "dump"})
		t.Cleanup(func() { d.setDepsMigration("ns1", nil) })
		require.True(t, d.longOp.TryLock(longOpMigration))
		t.Cleanup(d.longOp.Unlock)
		pre := decodePreflight(t, depsGet(mux, api.DependencyPreflightPath("postgres")))
		require.Len(t, pre.Problems, 1)
		assert.Contains(t, pre.Problems[0], "already running", "the journal's wording wins: it names the dependency")
	})
	// It answers WITHOUT Docker: there is no client on this daemon, and a
	// refusal that first tried to probe would 503 instead.
	t.Run("without touching docker", func(t *testing.T) {
		d, mux, _ := newDepsRoutesDaemon(t)
		require.Nil(t, d.active().dockerClient)
		require.True(t, d.longOp.TryLock(longOpMigration))
		t.Cleanup(d.longOp.Unlock)
		rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
		assert.Equal(t, http.StatusOK, rec.Code, "a 503 would mean it went looking for Docker")
	})
}

// Problems and Warnings are ARRAYS on the wire. The dialog maps over both
// without a nil guard, so a hand-built refusal that marshals `"warnings":null`
// crashes the confirm screen into the error boundary.
func TestRefusedPreflightMarshalsArraysNotNull(t *testing.T) {
	_, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"}, nil)
	rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"warnings":[]`)
	assert.NotContains(t, rec.Body.String(), "null")
}

// A refusal answered without Docker measured nothing, and its zeros must not
// be rendered as facts — see PreflightResult.Measured.
func TestARefusedPreflightReportsItMeasuredNothing(t *testing.T) {
	_, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}},
		&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18"}, nil)
	pre := decodePreflight(t, depsGet(mux, api.DependencyPreflightPath("postgres")))
	assert.False(t, pre.Measured())
	assert.Zero(t, pre.RequiredHostBytes)
}

func decodePreflight(t *testing.T, rec *httptest.ResponseRecorder) api.PreflightResult {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var pre api.PreflightResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pre))
	return pre
}

type fakeMigrator struct {
	pre    migrate.PreflightResult
	plan   *migrate.Plan
	err    error
	onPlan func(from, to string, opts migrate.PlanOptions)
	// supports stands in for the real migrator's pair verdict. Nil means "any
	// pair", which is what every test that is not about the verdict wants.
	supports func(from, to deps.Version) (bool, msg.Message)
	onProbe  func()
}

func (f fakeMigrator) SupportsPair(from, to deps.Version) (ok bool, problem msg.Message) {
	if f.supports == nil {
		return true, msg.Message{}
	}
	return f.supports(from, to)
}

func (f fakeMigrator) Preflight(context.Context, migrate.Env, migrate.Path) migrate.PreflightResult {
	if f.onProbe != nil {
		f.onProbe()
	}
	return f.pre
}

func (f fakeMigrator) Plan(_ context.Context, _ migrate.Env, path migrate.Path, opts migrate.PlanOptions) (*migrate.Plan, deps.MigrationJournal, error) {
	from, to := path.From(), path.To()
	if f.onProbe != nil {
		f.onProbe()
	}
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
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{
			pre: migrate.PreflightResult{OK: true, From: "postgres:17.5", To: "postgres:18"},
			onPlan: func(from, to string, opts migrate.PlanOptions) {
				planArgs.from, planArgs.to, planArgs.opts = from, to, opts
			},
			plan: &migrate.Plan{
				Steps: []migrate.Step{{ID: "only", Run: func(_ context.Context, _ *migrate.Journal, p migrate.StepProgress) error {
					holderDuringStep = d.longOp.Holder()
					p(50, msg.New("deps.msg.progress.analyzing"))
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
	// The broadcast event carries the MESSAGE; After is filled in per
	// subscriber by writeSSEEvent, in that subscriber's language.
	assert.Equal(t, "analyzing", englishForLogs.Render(prog.AfterMsg))

	assert.Nil(t, d.currentDepsMigration("ns1"), "progress state is cleared when the pass ends")
}

func TestMigrateReportsAFailedRunAsAnErrorEvent(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)

	rolledBack := make(chan struct{})
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
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
// Building the plan runs the whole preflight — a `du` of the data volume,
// minutes on a real cluster — after the click and before the 202. Without a
// published state that stretch is a dead button: no progress, no spinner, no
// event. StepCount stays 0, which is what tells a client "no plan yet".
func TestMigratePublishesPreparingBeforeThePlanExists(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)

	planning := make(chan struct{})
	release := make(chan struct{})
	var duringPlan *api.DependencyMigrationDto
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{
			onPlan: func(string, string, migrate.PlanOptions) {
				duringPlan = d.currentDepsMigration("ns1")
				close(planning)
				<-release
			},
			err: errors.New("preflight failed: not enough space"),
		}
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- depsPost(mux, api.DependencyMigratePath("postgres"), "{}") }()
	<-planning
	require.NotNil(t, duringPlan, "nothing was published while the plan was being built")
	assert.Equal(t, "postgres", duringPlan.ID)
	assert.Equal(t, api.DependencyMigrationStepPreparing, duringPlan.Step)
	assert.Zero(t, duringPlan.StepCount, "no plan yet — a client renders a spinner, not a list")

	close(release)
	rec := <-done
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	// The refusal is the answer to this request, so the preparing state goes
	// with it: a migration that is not happening must not be shown to every
	// client until the next one starts.
	assert.Nil(t, d.currentDepsMigration("ns1"))
}

// The CLI subscribes to the event stream BEFORE it posts, so the preparing
// state has to reach it as an event too — the 202 it is waiting for is exactly
// what the plan is delaying.
func TestMigrateBroadcastsPreparingBeforeThePlanExists(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)
	events, _, ok := d.addSubscriber()
	require.True(t, ok)
	t.Cleanup(func() { d.removeSubscriber(events) })
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{err: errors.New("preflight failed")}
	}

	depsPost(mux, api.DependencyMigratePath("postgres"), "{}")
	select {
	case evt := <-events:
		assert.Equal(t, api.EventDepsMigrationProgress, evt.Type)
		assert.Equal(t, api.DependencyMigrationStepPreparing, evt.Phase)
		assert.Equal(t, "postgres", evt.AppName)
		assert.Equal(t, "ns1", evt.NamespaceID)
	case <-time.After(2 * time.Second):
		t.Fatal("no preparing event reached a client that was already listening")
	}
}

func TestMigrateReportsAPlanRefusalSynchronously(t *testing.T) {
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
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
	d.depsMigratorFn = func(deps.ID) migrate.Migrator {
		return fakeMigrator{onProbe: func() {
			t.Error("the preflight must not probe while a migration is running")
		}}
	}

	rec := depsGet(mux, api.DependencyPreflightPath("postgres"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var pre api.PreflightResult
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

// --- a hop the DEPENDENCY'S OWN VENDOR forbids ------------------------------

// blockedRabbitDaemon is a namespace whose RabbitMQ data runs 4.1 against a
// bundle offering 4.3. RabbitMQ documents no one-step path for it — the
// operator has to go through 4.2 first — so nothing this launcher ships would
// make it work, and the refusal must say that instead of "update the
// launcher".
func blockedRabbitDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: {Image: "rabbitmq:4.1.8-management"},
	}, nil, nil)
	d.activeNs.dependencyUpgrades = []namespace.DependencyUpgrade{{
		ID: deps.RabbitMQ, App: "rabbitmq",
		From: "rabbitmq:4.1.8-management", To: "rabbitmq:4.3.5-management",
		Migratable: true, VendorBlocked: true, VendorVia: "4.2",
		Path: []string{"rabbitmq:4.1.8-management", "rabbitmq:4.3.5-management"},
	}}
	d.activeNs.dependencies = map[deps.ID]namespace.DependencyGen{
		deps.RabbitMQ: {Effective: "rabbitmq:4.1.8-management", Candidate: "rabbitmq:4.3.5-management"},
	}
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "rabbitmq"}}
	return d, mux
}

// The list row gets its OWN status and the vendor's own sentence. It must not
// borrow requires-launcher-update's words: that status means "a newer launcher
// will fix this", which here is a lie, and the operator has an actual next
// step (get onto 4.2 first) that the message has to name.
func TestAVendorBlockedHopGetsItsOwnStatusAndTheVendorsWords(t *testing.T) {
	_, mux := blockedRabbitDaemon(t)

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	require.Len(t, dto.Items, 1)
	assert.Equal(t, api.DependencyUpgradeBlocked, dto.Items[0].Status)
	assert.Contains(t, dto.Items[0].StatusDetail, "4.2", "the intermediate version is the whole point of the message")
	assert.Contains(t, dto.Items[0].StatusDetail, "citeck edit rabbitmq")
	assert.NotContains(t, dto.Items[0].StatusDetail, "update the launcher",
		"updating the launcher would not lift a refusal that belongs to RabbitMQ")
}

// Both routes refuse it, with the vendor's code and the vendor's sentence. The
// code is distinct from DEPENDENCY_NOT_MIGRATABLE because a client that maps
// the two together would offer "update the launcher" as the way out.
func TestBothRoutesRefuseAVendorBlockedHopWithoutBlamingTheLauncher(t *testing.T) {
	_, mux := blockedRabbitDaemon(t)

	for _, rec := range []*httptest.ResponseRecorder{
		depsGet(mux, api.DependencyPreflightPath("rabbitmq")),
		depsPost(mux, api.DependencyMigratePath("rabbitmq"), "{}"),
	} {
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyPairUnsupported)
		assert.NotContains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)
		assert.NotContains(t, rec.Body.String(), "update the launcher")
		assert.Contains(t, rec.Body.String(), "4.2")
	}
}

// The BANNER reads NamespaceDto.dependencyUpgrades, and it must be able to put
// a vendor-blocked hop in its own group rather than promising a migration.
// `blocked` is that discriminator and is NOT derivable from `migratable`,
// which is false for this pair too — a banner asking only that question would
// file it under "a newer launcher is needed" and send the operator to update
// one that would refuse it just the same.
func TestTheNamespaceDtoCarriesTheVendorRefusalForTheBanner(t *testing.T) {
	_, mux := blockedRabbitDaemon(t)

	dto := decodeNamespace(t, depsGet(mux, api.Namespace))
	require.Len(t, dto.DependencyUpgrades, 1)
	assert.False(t, dto.DependencyUpgrades[0].Migratable)
	assert.Contains(t, dto.DependencyUpgrades[0].Blocked, "4.2")
	assert.NotContains(t, dto.DependencyUpgrades[0].Blocked, "update the launcher")
}

// ZooKeeper data older than 3.5 may predate zookeeper.snapshot.trust.empty,
// where a newer server reads an empty snapshot as valid state. It is the same
// class of refusal — nothing to do with the launcher's age — and it must reach
// the operator with the property NAMED, because that string is what leads to
// the vendor's own explanation.
func TestZookeeperDataTooOldIsBlockedNotALauncherProblem(t *testing.T) {
	d, mux, rt := newDepsRoutesDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Zookeeper: {Image: "zookeeper:3.4.14"},
	}, nil, nil)
	d.activeNs.dependencyUpgrades = []namespace.DependencyUpgrade{{
		ID: deps.Zookeeper, App: "zookeeper", From: "zookeeper:3.4.14", To: "zookeeper:3.9.5",
		Migratable: true, VendorBlocked: true,
		Path: []string{"zookeeper:3.4.14", "zookeeper:3.9.5"},
	}}
	d.activeNs.dependencies = map[deps.ID]namespace.DependencyGen{
		deps.Zookeeper: {Effective: "zookeeper:3.4.14", Candidate: "zookeeper:3.9.5"},
	}

	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	require.Len(t, dto.Items, 1)
	assert.Equal(t, api.DependencyUpgradeBlocked, dto.Items[0].Status)
	assert.Contains(t, dto.Items[0].StatusDetail, "zookeeper.snapshot.trust.empty")
	assert.NotContains(t, dto.Items[0].StatusDetail, "update the launcher")

	rec := depsPost(mux, api.DependencyMigratePath("zookeeper"), "{}")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyPairUnsupported)
}

// The seam every surface shares. The list, the namespace DTO and both routes
// ask ONE function what a pair is worth, and it carries the REASON rather than
// a boolean precisely so the four cannot word the same refusal differently.
func TestPairProblemIsTheOneVerdictEverySurfaceAsksFor(t *testing.T) {
	d, _, _ := newDepsRoutesDaemon(t)

	assert.True(t, d.routeProblem(deps.RabbitMQ, migrate.Path{"rabbitmq:4.1.8-management", "rabbitmq:4.2.9-management"}).Empty(),
		"a hop the vendor allows and this launcher ships a plan for")
	assert.Contains(t, englishForLogs.Render(d.routeProblem(deps.RabbitMQ, migrate.Path{"rabbitmq:4.1.8-management", "rabbitmq:4.3.5-management"})), "4.2")
	assert.True(t, d.routeProblem(deps.Postgres, migrate.Path{"postgres:latest", "postgres:18"}).Empty(),
		"an unreadable tag is the preflight's to explain, and its message names the tag")
	assert.True(t, d.routeProblem(deps.MongoDB, migrate.Path{"mongo:4.0", "mongo:7.0"}).Empty(),
		"a dependency with no migrator is answered by the Migratable() arm, not by a second account of the pair")
}

// TestABundleThatNamesNoPostgresListsUpToDate is the user's own stand, driven
// end to end through the REAL generator rather than a hand-written fixture.
//
// Their bundle repo declares no postgres image anywhere, and the launcher still
// raised "Доступно обновление зависимости: postgres postgres:17.5 →
// postgres:18.6" on the dashboard: the 18.6 was generatePostgres' own fallback,
// so the only thing in the world asking for a new major was the launcher, and
// the offer named an upgrade nobody had chosen. With the default back on 17 the
// candidate equals the pin, nothing is held back, and every surface has to say
// so — the dependency list, and the banner the namespace DTO feeds.
//
// It is deliberately not written against `dependencies`/`dependencyUpgrades`
// literals: those two fields ARE the generator's verdict (namespace_loader.go
// copies them straight out of GenResp), so a fixture would restate the answer
// the bug was in and could not fail if the fallback moved again.
func TestABundleThatNamesNoPostgresListsUpToDate(t *testing.T) {
	nsCfg := &namespace.Config{
		ID:             "ns1",
		Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
		Proxy:          namespace.ProxyProps{Port: 80},
	}
	pins := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}
	// A bundle with a gateway and nothing else: the proxy hard-depends on the
	// gateway, and postgres is deliberately absent — that absence is the case.
	genResp, err := namespace.Generate(nsCfg,
		&bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppGateway: {Image: "citeck/gateway:1.0.0"}}},
		&bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppGateway}}},
		namespace.SystemSecrets{JWT: "j", OIDC: "o"},
		namespace.GenerateOpts{DependencyStates: pins})
	require.NoError(t, err)
	// assert, not require: the point of the test is what the ROUTES say, and a
	// hard stop here would leave every assertion below unexercised by the one
	// mutation that matters (putting the 18 fallback back).
	assert.Empty(t, genResp.DependencyUpgrades,
		"a bundle that names no postgres asks for nothing, so the generator may hold nothing back")

	rt := namespace.NewRuntime(nsCfg, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(pins, nil, nil)
	d := &Daemon{activeNs: &activeNamespace{
		runtime: rt, nsConfig: nsCfg, volumesBase: t.TempDir(),
		dependencies:       genResp.Dependencies,
		dependencyUpgrades: genResp.DependencyUpgrades,
	}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	var pg api.DependencyDto
	for _, it := range decodeDependencies(t, depsGet(mux, api.Dependencies)).Items {
		if it.ID == string(deps.Postgres) {
			pg = it
		}
	}
	require.Equal(t, string(deps.Postgres), pg.ID, "postgres must be listed at all")
	assert.Equal(t, api.DependencyUpToDate, pg.Status)
	assert.Empty(t, pg.StatusDetail, "nothing is held back, so there is nothing to explain")
	assert.Equal(t, "postgres:17.5", pg.CurrentImage)
	assert.Equal(t, "postgres:17.5", pg.TargetImage, "the target is what the namespace already runs")
	assert.Equal(t, "17.5", pg.CurrentVersion)
	assert.Equal(t, "17.5", pg.TargetVersion)

	assert.Empty(t, decodeNamespace(t, depsGet(mux, "/api/v1/namespace")).DependencyUpgrades,
		"and the dashboard banner the user actually saw stays down")
}
