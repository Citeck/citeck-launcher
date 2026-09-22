//go:build integration

package daemon

// Real-Docker tests for the dependency ROLLBACK: put one dependency back on the
// image AND the volume generation it ran on before its last completed
// migration.
//
// They are the only place the rollback is exercised end to end, and the only
// place its central promise is checked against real data rather than against a
// fake's map: the retained volume is still bootable, the namespace comes back
// on the OLD major with the data as of the migration, and everything written to
// the newer volume since then is still on disk and simply not read again.
//
// Unlike the migration tests, these drive the real ROUTE (`d.registerRoutes`
// over an httptest recorder) rather than a plan. The rollback is not a plan —
// it is three steps behind the long-operation lock, the journal blocker, the
// namespace-status gate and a preflight — and every one of those refusals is
// part of what the feature IS. A test that called runRollback directly would
// prove the three steps and none of the gates.
//
// ROOTLESS DOCKER: same requirement as the other tests in this build tag, and
// stated in the same words — see the header of deps_integration_test.go.

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
)

// itRowWrittenOn18 is a row that exists ONLY in the migrated cluster. It is
// what makes "the data is as of the migration" a measurement instead of a
// slogan: after the rollback it must be absent from the namespace, and still
// present in the retained newer volume.
const itRowWrittenOn18 = "written-on-18"

// migratePostgres runs the real 17 → 18 migration and requires that it
// committed. The assertions about WHAT it moved belong to
// TestIntegration_Postgres17To18; here it is the setup a rollback needs, and
// nothing more.
func (e *itEnv) migratePostgres(ctx context.Context, t *testing.T) {
	t.Helper()
	pre := migrate.PostgresMigrator{ID: deps.Postgres}.Preflight(ctx, e.env, migrate.Path{itFromImage, itToImage})
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	plan, journal, err := migrate.PostgresMigrator{ID: deps.Postgres}.Plan(ctx, e.env, migrate.Path{itFromImage, itToImage}, migrate.PlanOptions{})
	require.NoError(t, err)

	timer := newStepTimer()
	started := time.Now()
	require.NoError(t, migrate.Run(ctx, e.rt, journal, plan, timer.progress))
	timer.report(t)
	t.Logf("migration %s → %s took %s", itFromImage, itToImage, time.Since(started).Round(time.Millisecond))

	st := e.rt.DependencyStates()[deps.Postgres]
	require.Equal(t, itToImage, st.Image)
	require.Equal(t, 2, st.Gen())
	prev, has := st.Previous()
	require.True(t, has, "the commit records the state the rollback goes back to")
	require.Equal(t, itFromImage, prev.Image)
	require.Equal(t, 1, prev.Gen())
}

// runPostgres starts a temp container on one generation of the postgres volume,
// waits for the server and hands it to fn, then removes it.
//
// Every container these tests start goes through here, so a test can never
// leave one behind on a failed assertion: the removal is deferred, and it
// happens even when fn calls t.Fatal.
func (e *itEnv) runPostgres(ctx context.Context, t *testing.T, image string, gen int, fn func(container string)) {
	t.Helper()
	def, err := e.env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: image, VolumeGen: gen})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itCheckContainer})
	require.NoError(t, err)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	e.waitReady(ctx, t, itCheckContainer)
	fn(itCheckContainer)
}

// postgresRollbackOffer is what the dependency list route says about rolling
// postgres back — the offer the UI renders and the CLI prints.
func postgresRollbackOffer(t *testing.T, mux *http.ServeMux) *api.DependencyRollbackDto {
	t.Helper()
	return postgresItem(t, mux).Rollback
}

// TestIntegration_RollbackAfterPostgres17To18 migrates for real, writes into the
// new cluster, rolls back through the route, and asks the namespace what it is
// running.
func TestIntegration_RollbackAfterPostgres17To18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnv(t)
	e.seed(ctx, t)
	e.requireReadableCluster(ctx, t)
	e.migratePostgres(ctx, t)

	// A row that exists only on the newer generation. After the rollback the
	// namespace must not see it — and the volume that holds it must still be
	// there, because "retained, never deleted" is the promise the confirmation
	// makes.
	e.runPostgres(ctx, t, itToImage, 2, func(c string) {
		e.psql(ctx, t, c, "citeck_emodel", "INSERT INTO ecos_record(v) VALUES ('"+itRowWrittenOn18+"')")
		assert.Equal(t, "1", e.psql(ctx, t, c, "citeck_emodel",
			"SELECT count(*) FROM ecos_record WHERE v = '"+itRowWrittenOn18+"'"))
	})
	frozenBefore := e.volumeManifest(ctx, t, itVolume(2))
	require.NotEmpty(t, frozenBefore)

	// --- the offer ------------------------------------------------------------
	offer := postgresRollbackOffer(t, e.mux)
	require.NotNil(t, offer, "a namespace that has just migrated must be offered the way back")
	assert.True(t, offer.Available, "problem: %s", offer.Problem)
	assert.Equal(t, itFromImage, offer.ToImage)
	assert.Equal(t, itVolume(1), offer.Volume, "the retained volume is the one the migration copied FROM")
	assert.Equal(t, itVolume(2), offer.FrozenVolume, "the frozen volume is the one the namespace runs on today")
	assert.NotZero(t, offer.MigratedAt, "the offer is dated by the migration it undoes")

	// --- the rollback ---------------------------------------------------------
	rec := depsPost(e.mux, api.DependencyRollbackPath("postgres"), "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	e.d.bgWg.Wait()

	st := e.rt.DependencyStates()[deps.Postgres]
	assert.Equal(t, itFromImage, st.Image, "the namespace runs the previous image again")
	assert.Equal(t, 1, st.Gen(), "…on the previous volume generation, which is the half that matters")
	_, has := st.Previous()
	assert.False(t, has, "a rollback withdraws its own offer: there is no roll-forward")
	assert.Nil(t, e.rt.MigrationJournal(), "the rollback is not a journalled operation")
	assert.Nil(t, postgresRollbackOffer(t, e.mux), "…and the list route stops offering it")

	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, deps.ResultKindRollback, last.Kind, "a rollback and a migration share one result slot")
	assert.Equal(t, itToImage, last.From)
	assert.Equal(t, itFromImage, last.To)
	assert.Equal(t, itVolume(2), last.OldVolume, "the volume the operator may now reclaim is the NEWER one")
	// One reload for the migration's finalize, one for the rollback's own
	// restart — a rollback that did not reload would leave the namespace on the
	// generated files of the version it just left.
	assert.Equal(t, 2, e.reloads.get())

	// --- the newer volume is KEPT, and untouched ------------------------------
	assert.DirExists(t, e.volumeDir(itVolume(2)), "the rollback deletes nothing")
	assert.Equal(t, frozenBefore, e.volumeManifest(ctx, t, itVolume(2)),
		"the frozen volume is not read and not written after a rollback")
	pgv, err := e.env.ReadVolumeFile(ctx, itVolume(2), filepath.Join("18", "docker", "PG_VERSION"))
	require.NoError(t, err)
	assert.Equal(t, "18", strings.TrimSpace(pgv), "…and it still holds the 18 cluster")

	// --- what the namespace actually runs now ---------------------------------
	e.runPostgres(ctx, t, itFromImage, 1, func(c string) {
		assert.True(t, strings.HasPrefix(e.psql(ctx, t, c, "postgres", "SHOW server_version"), "17"),
			"the namespace serves the old major again")
		assert.Equal(t, "citeck_emodel\nciteck_keycloak\npostgres",
			e.psql(ctx, t, c, "postgres",
				"SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1"))
		assert.Equal(t, fmt.Sprint(itSeedRows), e.psql(ctx, t, c, "citeck_emodel",
			"SELECT count(*) FROM ecos_record"), "the pre-migration data is exactly as it was")
		assert.Equal(t, "0", e.psql(ctx, t, c, "citeck_emodel",
			"SELECT count(*) FROM ecos_record WHERE v = '"+itRowWrittenOn18+"'"),
			"everything written since the migration lives on the newer volume and is not read again")
	})

	// --- and it cannot be done twice ------------------------------------------
	// The pin no longer records a previous state, so the route refuses before
	// it touches Docker. This is the ordinary state of every namespace that has
	// never migrated, which is why it is a 409 and not an error.
	second := depsPost(e.mux, api.DependencyRollbackPath("postgres"), "")
	assert.Equal(t, http.StatusConflict, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), api.ErrCodeDependencyNoRollbackTarget)
}

// TestIntegration_RollbackRefusedWhenTheRetainedVolumeIsGone deletes the
// retained volume after a real migration.
//
// The launcher itself tells the operator they may reclaim that volume, so this
// is a state it actively creates — and a rollback that started anyway would
// stop the namespace and then discover there is nothing to switch back to.
func TestIntegration_RollbackRefusedWhenTheRetainedVolumeIsGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnv(t)
	e.seed(ctx, t)
	e.requireReadableCluster(ctx, t)
	e.migratePostgres(ctx, t)

	require.True(t, postgresRollbackOffer(t, e.mux).Available, "the offer stands while the volume is there")
	// Exactly what the operator does when they reclaim the space.
	require.NoError(t, e.env.RemoveVolume(ctx, itVolume(1)))
	require.NoDirExists(t, e.volumeDir(itVolume(1)))

	offer := postgresRollbackOffer(t, e.mux)
	require.NotNil(t, offer, "the offer is still SHOWN — with the reason it cannot be taken")
	assert.False(t, offer.Available)
	assert.Contains(t, offer.Problem, itVolume(1),
		"naming the volume is what makes the refusal actionable instead of mysterious")

	rec := depsPost(e.mux, api.DependencyRollbackPath("postgres"), "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyPreflightFailed)
	assert.Contains(t, rec.Body.String(), itVolume(1))

	// Nothing moved: the refusal is a refusal, not a half-done rollback.
	st := e.rt.DependencyStates()[deps.Postgres]
	assert.Equal(t, itToImage, st.Image)
	assert.Equal(t, 2, st.Gen())
	prev, has := st.Previous()
	assert.True(t, has, "the target is still recorded; only its volume is gone")
	assert.Equal(t, itFromImage, prev.Image)
	assert.DirExists(t, e.volumeDir(itVolume(2)), "the namespace still runs on the volume it migrated into")
	assert.Equal(t, 1, e.reloads.get(), "the refusal did not reload or restart the namespace")
}
