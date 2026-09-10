package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// newEditGateDaemon stands up a Daemon whose active namespace knows a pinned
// dependency (postgres, on 17.5) and one ordinary app. The namespace is
// STOPPED — the default for a fresh Runtime — so an accepted edit persists the
// patch without routing through a reload.
func newEditGateDaemon(t *testing.T, pins map[deps.ID]deps.DependencyState) (*Daemon, *http.ServeMux, *namespace.Runtime) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetGeneratedDefs([]appdef.ApplicationDef{
		{Name: "postgres", Image: "postgres:17.5"},
		{Name: "rabbitmq", Image: "rabbitmq:4.1.2-management"},
		{Name: "gateway", Image: "gw:1"},
	})
	rt.RestoreDependencyState(pins, nil, nil)
	d := &Daemon{activeNs: &activeNamespace{runtime: rt, nsConfig: &namespace.Config{ID: "ns1"}}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, rt
}

func newPinnedEditGateDaemon(t *testing.T) (*Daemon, *http.ServeMux, *namespace.Runtime) {
	t.Helper()
	return newEditGateDaemon(t, map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}})
}

func putAppConfig(mux *http.ServeMux, app, yamlBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+app+"/config", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestBreakingImageEditOnADependencyIsRefused(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.Contains(t, rec.Body.String(), "citeck deps upgrade postgres")
	assert.Nil(t, rt.AppPatch("postgres"), "a refused edit must not be persisted")
}

func TestNonBreakingImageEditOnADependencyIsAccepted(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.11\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}

func TestImageEditOnANonDependencyIsNotGated(t *testing.T) {
	_, mux, _ := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "gateway", "name: gateway\nimage: gw:2\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// With no pin there is no recorded version the data runs on, so there is
// nothing to refuse against: the gate must stay out of the way (this is the
// pre-pin namespace, before the first start seeds a pin).
func TestBreakingImageEditWithoutAPinIsNotGated(t *testing.T) {
	_, mux, rt := newEditGateDaemon(t, nil)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}

// An edit that leaves the image out entirely says nothing about the VERSION,
// which is the only thing this gate refuses, so it must pass even on a pinned
// dependency. What such a def then means is a separate question the gate
// deliberately does not answer: the editor round-trips the whole def, and
// ApplicationDef.Image carries no `omitempty`, so the stored patch records
// `image: ""` and the app ends up with a blank image — malformed, and caught
// at pull time, not silently started on data it does not fit.
func TestNonImageEditOnAPinnedDependencyIsNotGated(t *testing.T) {
	_, mux, rt := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nresources:\n  limits:\n    memory: 2g\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("postgres"))
}

// The gate is driven by each descriptor's OWN rule, not by postgres' — and
// postgres is the only one the tests above exercise. RabbitMQ and ZooKeeper
// are minor-breaking (the vendor supports next-minor upgrades only), so a
// move the postgres rule would wave through — same major, different minor —
// has to be refused here, while a patch-level bump inside the pinned minor is
// exactly the case the pin is supposed to let past.
func TestBreakingMinorImageEditOnADependencyIsRefused(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"}}

	_, mux, rt := newEditGateDaemon(t, pins)
	rec := putAppConfig(mux, "rabbitmq", "name: rabbitmq\nimage: rabbitmq:4.2.9-management\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.Contains(t, rec.Body.String(), "citeck deps upgrade rabbitmq")
	assert.Nil(t, rt.AppPatch("rabbitmq"), "a refused edit must not be persisted")

	_, mux2, rt2 := newEditGateDaemon(t, pins)
	rec2 := putAppConfig(mux2, "rabbitmq", "name: rabbitmq\nimage: rabbitmq:4.1.9-management\n")
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	assert.NotNil(t, rt2.AppPatch("rabbitmq"), "a same-minor bump is not a data migration")
}

// Every test above edits a STOPPED namespace, which takes the other half of
// handlePutAppConfig: a RUNNING one takes reloadMu and applies the edit by
// reloading the whole namespace, i.e. by regenerating and recreating
// containers from the very image the gate exists to refuse. So the refusal has
// to hold there too — nothing persisted, nothing reloaded, reloadMu left free.
func TestTheEditGateRefusesOnARunningNamespace(t *testing.T) {
	d, mux, rt := newPinnedEditGateDaemon(t)
	rt.SetStatusForTest(namespace.NsStatusRunning)
	reloads := 0
	d.reloadFn = func() error { reloads++; return nil }

	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.Zero(t, reloads, "a refused edit must not reload the namespace it was refused for")
	assert.Nil(t, rt.AppPatch("postgres"), "a refused edit must not be persisted")
	require.True(t, d.reloadMu.TryLock(), "a refused edit must not leave reloadMu held")
	d.reloadMu.Unlock()
}

// …and it must answer BEFORE the running path claims reloadMu. With a reload
// already in flight, a gate placed after that TryLock reports
// RELOAD_IN_PROGRESS — a refusal that reads as "try again in a minute" for an
// edit that will never be accepted, and that hides the one message telling the
// operator to run the migration instead.
func TestTheEditGateAnswersBeforeTheReloadLock(t *testing.T) {
	d, mux, rt := newPinnedEditGateDaemon(t)
	rt.SetStatusForTest(namespace.NsStatusRunning)
	d.reloadFn = func() error { return nil }
	d.reloadMu.Lock()
	t.Cleanup(d.reloadMu.Unlock)

	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18\n")

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.NotContains(t, rec.Body.String(), api.ErrCodeReloadInProgress,
		"the operator must be told the version is locked, not to wait for a reload")
	assert.Nil(t, rt.AppPatch("postgres"))
}

// newBackwardsEditGateDaemon is the namespace that HAS migrated: postgres runs
// on 18.6 out of volume postgres3, and the migration retained postgres2 on
// 17.5. The fake Env is returned so a test can take that retained volume away,
// or make Docker refuse to answer about it — the two facts that decide which
// way back the refusal is allowed to name.
func newBackwardsEditGateDaemon(t *testing.T) (*http.ServeMux, *namespace.Runtime, *migratetest.FakeEnv) {
	t.Helper()
	d, mux, rt := newEditGateDaemon(t, map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 1},
	})
	env := migratetest.New()
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	d.depsEnvFn = func(activeNamespace) migrate.Env { return env }
	return mux, rt, env
}

// A BACKWARDS breaking edit is refused like any other — the older binary
// cannot read the newer data — but the advice has to change with it. Sending
// the operator to `citeck deps upgrade` is sending them nowhere: that command
// refuses a backwards pair (DEPENDENCY_BACKWARDS), so the message would end a
// dead end of exactly the class this codebase keeps closing. The one deliberate
// way back is the rollback onto the volume the migration retained — and when
// that volume is really there, the message names it rather than hedging about
// whether it exists.
func TestABackwardsImageEditIsRefusedTowardsTheRollbackNotTheUpgrade(t *testing.T) {
	mux, rt, _ := newBackwardsEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.5\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"DEPENDENCY_VERSION_LOCKED"`)
	assert.Contains(t, rec.Body.String(), "citeck deps rollback postgres")
	assert.Contains(t, rec.Body.String(), "volume postgres2",
		"the way back is only actionable if it names the volume it goes back to")
	assert.NotContains(t, rec.Body.String(), "if it made one",
		"the daemon asked and got an answer; hedging here is a worse message than the fact")
	assert.NotContains(t, rec.Body.String(), "citeck deps upgrade")
	assert.Nil(t, rt.AppPatch("postgres"), "a refused edit must not be persisted")
}

// The launcher itself tells the operator they may reclaim the retained volume
// once they trust the new version, so a rollback offer whose volume is gone is
// a state it actively creates. Naming `citeck deps rollback` there would send
// them after a volume the launcher told them to delete — and the sentence that
// says so is migrate.RetainedVolumeGoneProblem, the same one the dependency
// list and the rollback preflight print, because three spellings of one fact
// drift the first time any of them is reworded.
func TestABackwardsEditSaysTheRetainedVolumeIsGoneWhenItIs(t *testing.T) {
	mux, _, env := newBackwardsEditGateDaemon(t)
	delete(env.Volumes, "postgres2")
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.5\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(),
		migrate.RetainedVolumeGoneProblem(deps.Postgres, "postgres2", "postgres:17.5"))
	assert.NotContains(t, rec.Body.String(), "citeck deps rollback",
		"a rollback whose volume is gone cannot be taken, so it must not be offered")
}

// A namespace that never migrated has no previous state at all, which is
// knowable without asking Docker anything. It gets the plain fact rather than a
// command that would refuse it (DEPENDENCY_NO_PREVIOUS) at the end of the trip.
func TestABackwardsEditWithNothingToGoBackToSaysSo(t *testing.T) {
	_, mux, _ := newEditGateDaemon(t, map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18.6", VolumeGen: 2},
	})
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.5\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "nothing to go back to")
	assert.NotContains(t, rec.Body.String(), "citeck deps rollback",
		"there is no retained volume, so there is no rollback to name")
}

// …and when the launcher could not ask at all — a Docker daemon that will not
// talk, which this very path can hit — it must degrade to the hedged wording,
// never to "the volume is gone". "I could not ask" is not "it is not there",
// and printing the second would tell the operator their data is destroyed on
// the strength of a socket error.
func TestABackwardsEditHedgesWhenDockerCannotAnswer(t *testing.T) {
	mux, _, env := newBackwardsEditGateDaemon(t)
	env.FailOn["volexists:postgres2"] = assert.AnError
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:17.5\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "citeck deps rollback postgres")
	assert.Contains(t, rec.Body.String(), "if it made one",
		"an unanswered question is reported as one, not as a fact")
	assert.NotContains(t, rec.Body.String(), "is gone")
}

// …and the FORWARD refusal keeps its own words, so the split cannot collapse
// into one sentence that is wrong half the time.
func TestAForwardBreakingEditStillNamesTheUpgrade(t *testing.T) {
	_, mux, _ := newPinnedEditGateDaemon(t)
	rec := putAppConfig(mux, "postgres", "name: postgres\nimage: postgres:18.6\n")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "citeck deps upgrade postgres")
	assert.NotContains(t, rec.Body.String(), "citeck deps rollback")
}

// A same-format backwards edit — a patch downgrade — is ACCEPTED, and that is
// a decision rather than a gap (user ruling, 2026-09-09: "патчи не надо
// откатывать. В патчах как правило все ок с совместимостью. Только «переломы»
// откатываем"). deps.Breaking keeps exactly its format semantics, so the
// generator applies such a bundle silently and the gate must not be stricter
// than the generator: refusing here would forbid by hand what the bundle does
// on its own.
func TestASameFormatPatchDowngradeEditIsAccepted(t *testing.T) {
	_, mux, rt := newEditGateDaemon(t, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: {Image: "rabbitmq:4.2.9-management"},
	})
	rec := putAppConfig(mux, "rabbitmq", "name: rabbitmq\nimage: rabbitmq:4.2.3-management\n")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, rt.AppPatch("rabbitmq"))
}
