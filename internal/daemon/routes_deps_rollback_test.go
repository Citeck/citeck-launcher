package daemon

import (
	"context"
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
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// migratedPin is the state of a namespace that has completed a 17.5 → 18.6
// migration: the pin is on generation 2 (volume postgres3) and carries the
// generation-1 state (volume postgres2) as its rollback target.
var migratedPin = deps.DependencyState{
	Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 1,
}

// newRollbackDaemon builds the post-migration namespace every test in this
// file starts from, with a FakeEnv standing in for Docker and the namespace
// lifecycle. The runtime is the authority on the pin (the route reads it
// there); the env's own copy is set to the same value because the preflight
// reads the CURRENT state through the env, exactly as production does.
// hookedEnv is a FakeEnv with one observation point the fake does not offer:
// what the world looked like AT the stop. The ordering of the stop and the pin
// write is the rollback's whole safety argument, so a test has to be able to
// stand between them.
type hookedEnv struct {
	*migratetest.FakeEnv
	onStop func()
}

func (e *hookedEnv) StopNamespace(ctx context.Context) error {
	if e.onStop != nil {
		e.onStop()
	}
	//nolint:wrapcheck // a test double must answer exactly what the fake answers
	return e.FakeEnv.StopNamespace(ctx)
}

// failPersister refuses every write, which is what a full disk (or a read-only
// state directory) looks like to the Runtime.
type failPersister struct{}

func (failPersister) SaveNamespaceState(string, string) error { return assert.AnError }

func newRollbackDaemon(t *testing.T) (*Daemon, *http.ServeMux, *namespace.Runtime, *hookedEnv) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: migratedPin}, nil,
		&deps.MigrationResult{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18.6",
			FinishedAt: time.UnixMilli(1757000000000), OldVolume: "postgres2"})

	fake := migratetest.New()
	env := &hookedEnv{FakeEnv: fake}
	env.States[deps.Postgres] = migratedPin
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	env.Volumes["postgres3"] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	env.LocalImages["postgres:17.5"] = true
	env.Running = true

	d := &Daemon{activeNs: &activeNamespace{
		runtime:      rt,
		nsConfig:     &namespace.Config{ID: "ns1"},
		volumesBase:  t.TempDir(),
		dockerClient: &docker.Client{},
		dependencies: map[deps.ID]namespace.DependencyGen{
			deps.Postgres: {Effective: "postgres:18.6", Candidate: "postgres:18.6"},
		},
	}}
	d.depsEnvFn = func(activeNamespace) migrate.Env { return env }
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, rt, env
}

// bundleOffers makes the bundle offer an older image, i.e. the held-back
// backwards candidate every "bundle-older" test is about.
func bundleOffers(d *Daemon, older string) {
	d.activeNs.dependencies[deps.Postgres] = namespace.DependencyGen{
		Effective: "postgres:18.6", Candidate: older,
	}
	d.activeNs.dependencyUpgrades = []namespace.DependencyUpgrade{{
		ID: deps.Postgres, App: "postgres", From: "postgres:18.6", To: older,
		Migratable: true, BundleOlder: true,
	}}
}

func postgresItem(t *testing.T, mux *http.ServeMux) api.DependencyDto {
	t.Helper()
	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	for _, it := range dto.Items {
		if it.ID == "postgres" {
			return it
		}
	}
	t.Fatal("postgres is not in the dependency list")
	return api.DependencyDto{}
}

// A bundle that offers an OLDER version across a data format is held back
// today and was reported as an "upgrade available" — the pairProblem carve-out
// answers "" for a downgrade, so the very first arm of heldUpgradeStatus
// claimed it. It is not an upgrade: nothing migrates data backwards, so the
// status and the sentence have to be their own.
func TestABackwardsBundleGetsItsOwnStatusAndNeitherUpgradeSentence(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	bundleOffers(d, "postgres:16.9")

	item := postgresItem(t, mux)
	assert.Equal(t, api.DependencyBundleOlder, item.Status)
	assert.Contains(t, item.StatusDetail, "older than the postgres:18.6 this namespace's data runs on")
	assert.NotContains(t, item.StatusDetail, "citeck deps upgrade")
	assert.NotContains(t, item.StatusDetail, "update the launcher")
}

// …and it is the FIRST question, before "does this launcher ship a migration
// for this dependency at all". A backwards bundle on a dependency nothing can
// migrate is still not an upgrade waiting on a newer launcher: there is
// nothing to wait for, ever.
func TestABackwardsBundleOnANonMigratableDependencyIsStillNotALauncherProblem(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	d.activeNs.dependencies[deps.Keycloak] = namespace.DependencyGen{
		Effective: "keycloak/keycloak:26.4.5", Candidate: "keycloak/keycloak:25.0.6",
	}
	d.activeNs.dependencyUpgrades = []namespace.DependencyUpgrade{{
		ID: deps.Keycloak, App: "keycloak", From: "keycloak/keycloak:26.4.5",
		To: "keycloak/keycloak:25.0.6", BundleOlder: true,
	}}
	dto := decodeDependencies(t, depsGet(mux, api.Dependencies))
	var kc api.DependencyDto
	for _, it := range dto.Items {
		if it.ID == "keycloak" {
			kc = it
		}
	}
	require.Equal(t, "keycloak", kc.ID)
	assert.Equal(t, api.DependencyBundleOlder, kc.Status)
	assert.NotEqual(t, api.DependencyRequiresLauncherUpdate, kc.Status)
	assert.Contains(t, kc.StatusDetail, "older than the keycloak/keycloak:26.4.5")
}

// The same state with ONE difference that changes the advice completely: the
// version the bundle offers is the one this namespace migrated FROM, so the
// volume that migration left is still on disk and going back is an action.
func TestABackwardsBundleThatMatchesTheRetainedVolumePointsAtTheRollback(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	bundleOffers(d, "postgres:17.2")

	item := postgresItem(t, mux)
	assert.Equal(t, api.DependencyBundleOlder, item.Status)
	assert.Contains(t, item.StatusDetail, "citeck deps rollback postgres")
	assert.NotContains(t, item.StatusDetail, "citeck deps upgrade")
}

// …and when the retained volume is GONE the rollback sentence would send the
// operator after a volume that no longer exists, so the plain notice is the
// honest one.
func TestABackwardsBundleWithNoRetainedVolumeDoesNotOfferTheRollback(t *testing.T) {
	d, mux, _, env := newRollbackDaemon(t)
	bundleOffers(d, "postgres:17.2")
	delete(env.Volumes, "postgres2")

	item := postgresItem(t, mux)
	assert.Equal(t, api.DependencyBundleOlder, item.Status)
	assert.NotContains(t, item.StatusDetail, "citeck deps rollback")
	require.NotNil(t, item.Rollback)
	assert.False(t, item.Rollback.Available)
	assert.Contains(t, item.Rollback.Problem, "postgres2")
}

// The offer itself: what it names, and that it is ABSENT rather than empty
// when the pin records no previous state.
func TestTheRollbackOfferNamesBothVolumesAndTheMigrationDate(t *testing.T) {
	_, mux, rt, _ := newRollbackDaemon(t)

	item := postgresItem(t, mux)
	require.NotNil(t, item.Rollback)
	assert.True(t, item.Rollback.Available)
	assert.Equal(t, "postgres:17.5", item.Rollback.ToImage)
	assert.Equal(t, "17.5", item.Rollback.ToVersion)
	assert.Equal(t, "postgres2", item.Rollback.Volume, "the retained volume it would run on")
	assert.Equal(t, "postgres3", item.Rollback.FrozenVolume, "the one it is leaving, kept and never read again")
	assert.Equal(t, int64(1757000000000), item.Rollback.MigratedAt)
	assert.Empty(t, item.Rollback.Problem)

	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18.6", VolumeGen: 2},
	}, nil, nil)
	assert.Nil(t, postgresItem(t, mux).Rollback, "no previous state means no offer at all")
}

// A rollback VERDICT in the one result slot must not be read as the date of
// the migration it failed to undo: the offer is still live (the pin never
// moved), and its sentence would then be dated by the failure.
func TestTheMigrationDateIsNotTakenFromARollbackVerdict(t *testing.T) {
	_, mux, rt, _ := newRollbackDaemon(t)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: migratedPin}, nil,
		&deps.MigrationResult{ID: deps.Postgres, From: "postgres:18.6", To: "postgres:17.5",
			FinishedAt: time.UnixMilli(1758000000000), Kind: deps.ResultKindRollback})

	item := postgresItem(t, mux)
	require.NotNil(t, item.Rollback)
	assert.Zero(t, item.Rollback.MigratedAt)
}

// `citeck deps upgrade postgres` on a backwards pair finds a pending entry and
// used to reach the preflight, which refuses it with the right sentence in the
// wrong place. Both routes refuse it here, with a code of its own: the two
// that were reachable before both lie (one says a newer launcher would help,
// the other reports a vendor refusal to a question nobody asked).
func TestBothMigrationRoutesRefuseABackwardsPairAsBackwards(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	bundleOffers(d, "postgres:17.2")

	for _, rec := range []*httptest.ResponseRecorder{
		depsPost(mux, api.DependencyMigratePath("postgres"), "{}"),
		depsGet(mux, api.DependencyPreflightPath("postgres")),
	} {
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyBackwards)
		assert.Contains(t, rec.Body.String(), "citeck deps rollback postgres")
		assert.NotContains(t, rec.Body.String(), api.ErrCodeDependencyNotMigratable)
		assert.NotContains(t, rec.Body.String(), api.ErrCodeDependencyPairUnsupported)
	}
}

// The banner reads the NAMESPACE dto, and a backwards hold has the exact shape
// of "a newer launcher is needed" there: migratable false, blocked empty. So
// it has to carry the flag, or the operator is sent to update a launcher that
// would never apply it.
func TestTheNamespaceDtoMarksABackwardsHoldForTheBanner(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	bundleOffers(d, "postgres:17.2")

	dto := decodeNamespace(t, depsGet(mux, api.Namespace))
	require.Len(t, dto.DependencyUpgrades, 1)
	assert.True(t, dto.DependencyUpgrades[0].BundleOlder)
	assert.False(t, dto.DependencyUpgrades[0].Migratable)
	assert.Empty(t, dto.DependencyUpgrades[0].Blocked)
}

// The happy path, and the ordering rule that is the whole reason the rollback
// takes a long-op lock: the namespace must be STOPPED before the pin moves.
// syncDependencyPinsUnderLock re-pins any dependency whose app is RUNNING on
// an image other than its pin, so a pin written while the 18 container is up
// is re-pinned forward on the next loop tail WHILE THE GENERATION STAYS BACK —
// postgres:18.6 mounted on the generation-1 volume that holds 17 data.
func TestRollbackStopsTheNamespaceBeforeItMovesThePin(t *testing.T) {
	d, mux, rt, env := newRollbackDaemon(t)
	var pinWhenStopped string
	var kindWhenStopped string
	env.onStop = func() {
		pinWhenStopped = rt.DependencyPins()[deps.Postgres]
		if live := d.currentDepsMigration("ns1"); live != nil {
			kindWhenStopped = live.Kind
		}
	}

	rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	d.bgWg.Wait()

	assert.Equal(t, "postgres:18.6", pinWhenStopped,
		"the stop must come FIRST — a pin written under a running container is re-pinned forward")
	assert.Equal(t, []string{"stopns", "reload:true"}, env.Log())
	st := rt.DependencyStates()[deps.Postgres]
	assert.Equal(t, "postgres:17.5", st.Image)
	assert.Equal(t, 1, st.Gen(), "the generation goes back too, or the old image mounts the new volume")
	assert.Empty(t, st.PrevImage, "a rollback withdraws its own offer: there is no roll-forward")
	assert.Equal(t, []bool{true}, env.Reloads(), "the namespace was running, so it is started again")
	assert.Equal(t, deps.ResultKindRollback, kindWhenStopped,
		"the live progress must say which of the two operations is on the shared channel")
}

// The verdict, the progress channel and the events. A rollback rides the
// migration's channel with a three-step list and a Kind, so the CLI's renderer
// and the dialog's progress screen work unchanged.
func TestRollbackRecordsItsVerdictAndReportsThreeSteps(t *testing.T) {
	d, mux, rt, _ := newRollbackDaemon(t)
	events, _, ok := d.addSubscriber()
	require.True(t, ok)
	t.Cleanup(func() { d.removeSubscriber(events) })

	require.Equal(t, http.StatusAccepted, depsPost(mux, api.DependencyRollbackPath("postgres"), "").Code)
	d.bgWg.Wait()

	res := rt.LastDependencyMigration()
	require.NotNil(t, res)
	assert.Equal(t, deps.ResultKindRollback, res.Kind)
	assert.Equal(t, "postgres:18.6", res.From)
	assert.Equal(t, "postgres:17.5", res.To)
	assert.Equal(t, "postgres3", res.OldVolume, "the frozen volume is what the operator may reclaim")
	assert.Empty(t, res.Error)

	var steps []string
	var kinds []string
	for len(events) > 0 {
		e := <-events
		if e.Type == api.EventDepsMigrationProgress && e.Phase != api.DependencyMigrationStepPreparing {
			steps = append(steps, e.Phase)
			assert.Equal(t, 3, e.Total)
		}
		kinds = append(kinds, e.Type)
	}
	assert.Equal(t, migrate.RollbackStepIDs(), steps)
	// …and the discriminator reaches the wire, on both the live progress and
	// the verdict: they share the migration's channel and its one result slot,
	// so "18.6 → 17.5 finished" is otherwise indistinguishable from a migration.
	assert.Equal(t, deps.ResultKindRollback,
		decodeDependencies(t, depsGet(mux, api.Dependencies)).LastResult.Kind)
	assert.Contains(t, kinds, api.EventDepsMigrationStart)
	assert.Contains(t, kinds, api.EventDepsMigrationComplete)
	assert.NotContains(t, kinds, api.EventDepsMigrationError)
	assert.Nil(t, d.currentDepsMigration("ns1"), "the progress state is cleared when the pass ends")
	assert.Equal(t, longOpNone, d.longOp.Holder())
}

// The lock has to be claimed AS a rollback: labeled a plain request it would
// be a member of tolerateLifecycleWork, and a namespace Start landing between
// the stop and the pin write is precisely the race the stop exists to close.
func TestTheRollbackHoldsItsOwnLongOpKind(t *testing.T) {
	d, mux, _, env := newRollbackDaemon(t)
	var holder longOpKind
	env.onStop = func() { holder = d.longOp.Holder() }

	require.Equal(t, http.StatusAccepted, depsPost(mux, api.DependencyRollbackPath("postgres"), "").Code)
	d.bgWg.Wait()

	assert.Equal(t, longOpDepsRollback, holder)
	assert.Equal(t, "a dependency rollback is running", longOpDepsRollback.busyEnglish())
	assert.Contains(t, englishForLogs.Render(longOpDepsRollback.busyMessage()),
		"a dependency rollback is running")
	assert.False(t, tolerateLifecycleWork.allows(longOpDepsRollback),
		"a start between the stop and the pin write is exactly what this refuses")
	assert.False(t, tolerateNothing.allows(longOpDepsRollback))
}

// A pin write that never reached disk leaves the namespace stopped on the OLD
// pin. The route puts it back the way it found it and reports the error: the
// alternative is a namespace the operator has to start by hand after an action
// that did nothing.
func TestARefusedPinWriteRestartsTheNamespaceOnTheOldPin(t *testing.T) {
	d, mux, rt, env := newRollbackDaemon(t)
	rt.SetStatePersister(failPersister{})
	events, _, ok := d.addSubscriber()
	require.True(t, ok)
	t.Cleanup(func() { d.removeSubscriber(events) })

	require.Equal(t, http.StatusAccepted, depsPost(mux, api.DependencyRollbackPath("postgres"), "").Code)
	d.bgWg.Wait()

	assert.Equal(t, "postgres:18.6", rt.DependencyPins()[deps.Postgres], "a refused write moves nothing")
	assert.Equal(t, []bool{true}, env.Reloads(), "the namespace is put back the way it was found")
	var types []string
	for len(events) > 0 {
		types = append(types, (<-events).Type)
	}
	assert.Contains(t, types, api.EventDepsMigrationError)
	assert.NotContains(t, types, api.EventDepsMigrationComplete)
}

// Every refusal the rollback shares with a migration, and the one that is its
// own: a pin with no recorded previous state.
func TestRollbackRouteRefusals(t *testing.T) {
	t.Run("unknown dependency", func(t *testing.T) {
		_, mux, _, _ := newRollbackDaemon(t)
		rec := depsPost(mux, api.DependencyRollbackPath("banana"), "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyUnknown)
	})
	t.Run("no rollback target", func(t *testing.T) {
		_, mux, rt, _ := newRollbackDaemon(t)
		rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
			deps.Postgres: {Image: "postgres:18.6", VolumeGen: 2},
		}, nil, nil)
		rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNoRollbackTarget)
	})
	t.Run("open journal", func(t *testing.T) {
		_, mux, rt, _ := newRollbackDaemon(t)
		rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: migratedPin},
			&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:18.6", To: "postgres:19"}, nil)
		rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress)
	})
	t.Run("namespace mid-transition", func(t *testing.T) {
		_, mux, rt, _ := newRollbackDaemon(t)
		rt.SetStatusForTest(namespace.NsStatusStopping)
		rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyNamespaceBusy)
	})
	t.Run("another long operation", func(t *testing.T) {
		d, mux, _, _ := newRollbackDaemon(t)
		require.True(t, d.longOp.TryLock(longOpSnapshot))
		t.Cleanup(d.longOp.Unlock)
		rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
		assert.Contains(t, rec.Body.String(), "a snapshot is in progress")
	})
	t.Run("retained volume gone", func(t *testing.T) {
		_, mux, rt, env := newRollbackDaemon(t)
		delete(env.Volumes, "postgres2")
		rec := depsPost(mux, api.DependencyRollbackPath("postgres"), "")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyPreflightFailed)
		assert.Contains(t, rec.Body.String(), "postgres2")
		assert.Equal(t, "postgres:18.6", rt.DependencyPins()[deps.Postgres])
	})
}

// The preflight route answers the three consequences the operator confirms,
// measures nothing, and reports a daemon-side refusal as a problem on the
// confirm screen rather than as a 409 after the click.
func TestRollbackPreflightRouteAnswersTheConsequences(t *testing.T) {
	_, mux, _, _ := newRollbackDaemon(t)
	pre := decodePreflight(t, depsGet(mux, api.DependencyRollbackPreflightPath("postgres")))
	require.True(t, pre.OK, "%v", pre.Problems)
	assert.False(t, pre.Measured(), "a rollback creates nothing, so it measures nothing")
	assert.Equal(t, "postgres:18.6", pre.From)
	assert.Equal(t, "postgres:17.5", pre.To)
	joined := strings.Join(pre.Warnings, "\n")
	assert.Contains(t, joined, "postgres2")
	assert.Contains(t, joined, "postgres3")
	assert.Contains(t, joined, "no roll-forward")
}

// The confirm screen's own input must describe the action the operator
// clicked: "start or stop it before migrating" on a Roll back screen sends
// them looking for a migration that is not happening.
func TestRollbackPreflightNamesTheRollbackNotAMigration(t *testing.T) {
	_, mux, rt, _ := newRollbackDaemon(t)
	rt.SetStatusForTest(namespace.NsStatusStarting)
	pre := decodePreflight(t, depsGet(mux, api.DependencyRollbackPreflightPath("postgres")))
	assert.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)
	assert.Contains(t, pre.Problems[0], "before rolling it back")
	assert.NotContains(t, pre.Problems[0], "migrating")
}

func TestRollbackPreflightReportsALongOperationAsAProblem(t *testing.T) {
	d, mux, _, _ := newRollbackDaemon(t)
	require.True(t, d.longOp.TryLock(longOpSnapshot))
	t.Cleanup(d.longOp.Unlock)
	pre := decodePreflight(t, depsGet(mux, api.DependencyRollbackPreflightPath("postgres")))
	assert.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)
	assert.Contains(t, pre.Problems[0], "a snapshot is in progress")
}

// Two surfaces answer "why can this rollback not be taken": the dependency
// LIST, which judges every offer on every request without building a
// preflight, and the preflight itself once the user clicks through. They are
// the same sentence to the same operator, so they must be the same STRING —
// which they are only because both call migrate.RetainedVolumeGoneProblem.
// Two spellings would drift the first time either was reworded, and the
// divergence would be invisible: each surface reads correctly on its own.
func TestTheListAndThePreflightNameAMissingRetainedVolumeIdentically(t *testing.T) {
	_, mux, _, env := newRollbackDaemon(t)
	delete(env.Volumes, "postgres2")

	item := postgresItem(t, mux)
	require.NotNil(t, item.Rollback)
	require.False(t, item.Rollback.Available)

	pre := decodePreflight(t, depsGet(mux, api.DependencyRollbackPreflightPath("postgres")))
	require.False(t, pre.OK)
	require.Len(t, pre.Problems, 1)

	// Both surfaces are compared as RENDERED sentences, because that is what
	// each one actually sends: the list's Problem and the preflight's
	// Problems[0] are strings on the wire, and the point of the test is that
	// they are the SAME sentence.
	want := englishForLogs.Render(migrate.RetainedVolumeGoneProblem(deps.Postgres, "postgres2", "postgres:17.5"))
	assert.Equal(t, want, item.Rollback.Problem)
	assert.Equal(t, want, pre.Problems[0])
}
