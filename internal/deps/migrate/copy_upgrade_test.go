package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"

	"github.com/citeck/citeck-launcher/internal/msg"
)

const (
	rabbitFrom = "rabbitmq:4.1.2-management"
	rabbitTo   = "rabbitmq:4.2.9-management"
)

func rabbitDescriptor() deps.Descriptor {
	d, ok := deps.Lookup(deps.RabbitMQ)
	if !ok {
		panic("rabbitmq is not registered")
	}
	return d
}

var (
	rabbitGen1 = deps.VolumeName(rabbitDescriptor(), 1) // rabbitmq2
	rabbitGen2 = deps.VolumeName(rabbitDescriptor(), 2) // rabbitmq3
)

// countInventory is a stand-in for a real dependency's "before" picture: one
// number that must survive, plus one that is allowed to differ (ZooKeeper's
// ephemerals are the real case).
type countInventory struct {
	kept     int
	fleeting int
}

func (a countInventory) Diff(after Inventory) (problems, notes []string) {
	b, ok := after.(countInventory)
	if !ok {
		return []string{"inventory type changed"}, nil
	}
	if a.kept != b.kept {
		problems = append(problems, fmt.Sprintf("kept %d → %d", a.kept, b.kept))
	}
	if a.fleeting != b.fleeting {
		notes = append(notes, fmt.Sprintf("fleeting %d → %d", a.fleeting, b.fleeting))
	}
	return problems, notes
}

// copyCalls records what the shared plan asked a dependency's hooks to do, in
// order, so a test can assert that the pre-upgrade hook ran on the OLD image
// and the post-upgrade one on the NEW image — the ordering ruling 1 rests on.
type copyCalls struct{ log []string }

// testSpec is a minimal dependency: a readiness probe, both hooks, and an
// inventory whose numbers come from a per-container map the test controls.
func testSpec(calls *copyCalls, inv map[string]countInventory) CopySpec {
	return CopySpec{
		ID:        deps.RabbitMQ,
		TempEnv:   map[string]string{"RABBITMQ_NODENAME": "rabbit@rabbitmq"},
		HostAlias: map[string]string{"rabbitmq": "127.0.0.1"},
		WaitReady: func(_ context.Context, env Env, container string, _ StepProgress) error {
			running, err := env.ContainerRunning(context.Background(), container)
			if err != nil {
				return fmt.Errorf("check %s: %w", container, err)
			}
			if !running {
				return fmt.Errorf("%s is not running", container)
			}
			calls.log = append(calls.log, "ready:"+container)
			return nil
		},
		PreUpgrade: func(_ context.Context, env Env, container string, _ StepProgress) error {
			def, _ := env.(*guardEnv).ContainerDef(container)
			calls.log = append(calls.log, "pre:"+container+":"+def.Image)
			return nil
		},
		PostUpgrade: func(_ context.Context, env Env, container string, _ StepProgress) error {
			def, _ := env.(*guardEnv).ContainerDef(container)
			calls.log = append(calls.log, "post:"+container+":"+def.Image)
			return nil
		},
		Inventory: func(_ context.Context, _ Env, container string) (Inventory, error) {
			calls.log = append(calls.log, "inv:"+container)
			return inv[container], nil
		},
	}
}

// guardEnv is the structural half of "the source volume is only ever read".
// It wraps the fake and FAILS THE TEST on any call that could write to the
// source volume — creating it, removing it, copying INTO it, making
// directories in it, or starting a container that mounts it. Every copy-plan
// test runs against it, so the invariant is enforced by the harness rather
// than by a reviewer noticing.
type guardEnv struct {
	*migratetest.FakeEnv
	t   *testing.T
	src string
}

func newGuardEnv(t *testing.T, f *migratetest.FakeEnv, src string) *guardEnv {
	t.Helper()
	return &guardEnv{FakeEnv: f, t: t, src: src}
}

func (g *guardEnv) refuse(op string) {
	g.t.Helper()
	g.t.Fatalf("the copy-upgrade plan touched the SOURCE volume %s for writing (%s)", g.src, op)
}

func (g *guardEnv) CreateVolume(ctx context.Context, v string) error {
	if v == g.src {
		g.refuse("CreateVolume")
	}
	return g.FakeEnv.CreateVolume(ctx, v) //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

func (g *guardEnv) RemoveVolume(ctx context.Context, v string) error {
	if v == g.src {
		g.refuse("RemoveVolume")
	}
	return g.FakeEnv.RemoveVolume(ctx, v) //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

func (g *guardEnv) CopyVolume(ctx context.Context, src, dst string) error {
	if dst == g.src {
		g.refuse("CopyVolume destination")
	}
	return g.FakeEnv.CopyVolume(ctx, src, dst) //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

func (g *guardEnv) EnsureVolumeDirs(ctx context.Context, v string, dirs []string) error {
	if v == g.src {
		g.refuse("EnsureVolumeDirs")
	}
	return g.FakeEnv.EnsureVolumeDirs(ctx, v, dirs) //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

func (g *guardEnv) RunAppDef(ctx context.Context, def appdef.ApplicationDef, opts deps.TempContainerOpts) (string, error) {
	for _, v := range def.Volumes {
		if strings.HasPrefix(v, g.src+":") {
			g.refuse("RunAppDef mounts it in " + opts.Name)
		}
	}
	return g.FakeEnv.RunAppDef(ctx, def, opts) //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

// rabbitEnv is a stopped namespace pinned at generation 1 with data in it.
func rabbitEnv(t *testing.T) *guardEnv {
	t.Helper()
	f := migratetest.New()
	f.Volumes[rabbitGen1] = map[string]string{"mnesia/rabbit@rabbitmq/DECISION_TAB.LOG": "x"}
	f.VolSize[rabbitGen1] = 512 << 20
	f.States[deps.RabbitMQ] = deps.DependencyState{Image: rabbitFrom}
	return newGuardEnv(t, f, rabbitGen1)
}

func buildCopyPlan(t *testing.T, env Env, spec CopySpec) (*Plan, deps.MigrationJournal) {
	t.Helper()
	pre, _, ok := CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, rabbitTo,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.True(t, ok, pre.Problems)
	pre.OK = len(pre.Problems) == 0
	plan, j, err := BuildCopyUpgrade(env, spec, rabbitFrom, rabbitTo, PlanOptions{}, pre)
	require.NoError(t, err)
	return plan, j
}

// The step list IS the contract: the ids are locale keys in the CLI and the
// web dialog, and the order is what makes each step's rollback obligation
// true — nothing is created before the journal claims it, and the old image
// runs on the copy before the new one does.
func TestCopyUpgradeStepsAreTheDocumentedOrder(t *testing.T) {
	env := rabbitEnv(t)
	plan, _ := buildCopyPlan(t, env, testSpec(&copyCalls{}, nil))
	ids := make([]string, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		ids = append(ids, st.ID)
	}
	assert.Equal(t, []string{
		"stop-namespace", "pull-image", "create-volume", "copy-volume",
		"start-old", "pre-upgrade", "stop-old", "start-new", "post-upgrade",
		"verify", "stop-new",
	}, ids)
	assert.Equal(t, CopyStepIDs(), ids,
		"CopyStepIDs is what the CLI and the dialog translate; it has to BE the plan")
}

// The whole safety story: the original volume is mounted exactly ONCE, by the
// copy, and everything else in the plan runs on the copy. guardEnv fails the
// test on any write to the source, so what is left to assert here is the
// positive half — every container really did land on the new generation.
func TestACopyUpgradeRunsEverythingOnTheCopy(t *testing.T) {
	env := rabbitEnv(t)
	calls := &copyCalls{}
	plan, j := buildCopyPlan(t, env, testSpec(calls, nil))
	st := &fakeStore{}
	require.NoError(t, Run(context.Background(), st, j, plan, nil))

	log := strings.Join(env.Log(), "\n")
	assert.Contains(t, log, "copy:"+rabbitGen1+"->"+rabbitGen2)
	assert.Equal(t, 1, strings.Count(log, rabbitGen1),
		"the source volume is named exactly once in the whole run, by the copy")
	for _, name := range []string{SrcContainer, DstContainer} {
		def, ok := env.ContainerDef(name)
		if !ok {
			// gone by the end of a successful run; the guard already checked
			// every def at start time, so absence here is expected.
			continue
		}
		assert.Equal(t, []string{rabbitGen2 + ":/data"}, def.Volumes, "%s", name)
	}
	assert.Equal(t, deps.DependencyState{Image: rabbitTo, VolumeGen: 2}, st.pin)
	require.Len(t, st.commits, 1)
	assert.Equal(t, rabbitGen1, st.commits[0].OldVolume,
		"the verdict names the volume the operator can now reclaim")
}

// Write-ahead: the journal claims the volume BEFORE it exists, so a crash
// between the two cannot leave a volume the rollback does not know about.
func TestCopyUpgradeJournalsTheVolumeBeforeItCreatesIt(t *testing.T) {
	env := rabbitEnv(t)
	plan, j := buildCopyPlan(t, env, testSpec(&copyCalls{}, nil))
	st := &fakeStore{onSet: func(rec deps.MigrationJournal) {
		if rec.CreatedVolume != "" {
			env.Record("journal:" + rec.CreatedVolume)
		}
	}}
	require.NoError(t, Run(context.Background(), st, j, plan, nil))
	joined := strings.Join(env.Log(), "\n")
	require.Contains(t, joined, "journal:"+rabbitGen2)
	assert.Less(t, strings.Index(joined, "journal:"+rabbitGen2), strings.Index(joined, "createvol:"+rabbitGen2))
	assert.Less(t, strings.Index(joined, "createvol:"+rabbitGen2), strings.Index(joined, "copy:"))
}

// The preflight's refusal is not enough: a volume can appear between the
// preflight and the step. The step re-checks and refuses rather than deleting
// data nobody agreed to lose — and because it journaled nothing, the rollback
// leaves that volume alone too.
func TestCopyCreateVolumeRefusesAVolumeThatAppearedAfterThePreflight(t *testing.T) {
	env := rabbitEnv(t)
	plan, j := buildCopyPlan(t, env, testSpec(&copyCalls{}, nil))
	env.Volumes[rabbitGen2] = map[string]string{"someone-elses": "data"}

	st := &fakeStore{}
	err := Run(context.Background(), st, j, plan, nil)
	require.ErrorContains(t, err, "already exists")
	assert.Contains(t, env.Volumes, rabbitGen2, "somebody else's data is not deleted by the rollback")
	assert.NotContains(t, strings.Join(env.Log(), "\n"), "rmvol:"+rabbitGen2)
	assert.Empty(t, st.pin)
}

// Ruling 1: the pre-upgrade hook runs on the OLD image and the post-upgrade
// one on the NEW image, both on the copy, with the "before" inventory taken
// between them. Getting the images the wrong way round is the difference
// between enabling the required feature flags a new node refuses to start
// without and enabling nothing at all.
func TestCopyUpgradeRunsEachHookOnItsOwnImage(t *testing.T) {
	env := rabbitEnv(t)
	calls := &copyCalls{}
	plan, j := buildCopyPlan(t, env, testSpec(calls, nil))
	require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	assert.Equal(t, []string{
		"ready:" + SrcContainer,
		"pre:" + SrcContainer + ":" + rabbitFrom,
		"inv:" + SrcContainer,
		"ready:" + DstContainer,
		"post:" + DstContainer + ":" + rabbitTo,
		"inv:" + DstContainer,
	}, calls.log)
}

func j2store() *fakeStore { return &fakeStore{} }

// The verify compares the two live inventories. A difference the dependency
// calls a PROBLEM fails the step (and rolls the whole thing back); one it
// calls a NOTE does not — ZooKeeper's ephemeral nodes expire while the copy
// runs with no clients, and a strict comparison would fail every real
// migration.
func TestCopyUpgradeVerifyFailsOnALostObjectAndToleratesTheRest(t *testing.T) {
	t.Run("a lost object fails", func(t *testing.T) {
		env := rabbitEnv(t)
		inv := map[string]countInventory{
			SrcContainer: {kept: 7, fleeting: 3},
			DstContainer: {kept: 6, fleeting: 3},
		}
		plan, j := buildCopyPlan(t, env, testSpec(&copyCalls{}, inv))
		st := &fakeStore{}
		err := Run(context.Background(), st, j, plan, nil)
		require.ErrorContains(t, err, "kept 7 → 6")
		assert.Empty(t, st.pin)
		assert.NotContains(t, env.Volumes, rabbitGen2, "the rollback removed the copy")
	})
	t.Run("a tolerated difference passes", func(t *testing.T) {
		env := rabbitEnv(t)
		inv := map[string]countInventory{
			SrcContainer: {kept: 7, fleeting: 3},
			DstContainer: {kept: 7, fleeting: 0},
		}
		plan, j := buildCopyPlan(t, env, testSpec(&copyCalls{}, inv))
		require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	})
}

// EnsureDirs exist because a temp container runs the container and nothing
// around it — no init container to make ZooKeeper's data directories. They go
// on the COPY, before anything is started on it.
func TestEnsureDirsAreCreatedOnTheCopyBeforeAnythingStarts(t *testing.T) {
	env := rabbitEnv(t)
	spec := testSpec(&copyCalls{}, nil)
	spec.EnsureDirs = []string{"data", "datalog"}
	plan, j := buildCopyPlan(t, env, spec)
	require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	assert.Equal(t, []string{"data", "datalog"}, env.VolumeDirs(rabbitGen2))
	joined := strings.Join(env.Log(), "\n")
	assert.Less(t, strings.Index(joined, "voldirs:"+rabbitGen2), strings.Index(joined, "run:"+SrcContainer))
}

// The rollback undoes what the journal says was done and nothing else: the
// source volume is never in the journal, so it can never be removed here. It
// is idempotent (the boot recovery may run it against work already undone),
// it reports every failure it hit, and it hands the namespace back running
// only when the cleanup left nothing behind.
func TestRollbackCopyUpgradeRemovesOnlyWhatItMade(t *testing.T) {
	ctx := context.Background()
	t.Run("removes the copy and restarts", func(t *testing.T) {
		env := rabbitEnv(t)
		env.Volumes[rabbitGen2] = map[string]string{}
		env.Running = false
		j := &deps.MigrationJournal{ID: deps.RabbitMQ, From: rabbitFrom, To: rabbitTo,
			CreatedVolume: rabbitGen2, SourceVolume: rabbitGen1, WasRunning: true}
		require.NoError(t, RollbackCopyUpgrade(ctx, env, j))
		assert.NotContains(t, env.Volumes, rabbitGen2)
		assert.Contains(t, env.Volumes, rabbitGen1)
		assert.Equal(t, []bool{true}, env.Reloads())
		// idempotent
		require.NoError(t, RollbackCopyUpgrade(ctx, env, j))
	})
	t.Run("a volume it did not journal is not removed", func(t *testing.T) {
		env := rabbitEnv(t)
		env.Volumes[rabbitGen2] = map[string]string{}
		j := &deps.MigrationJournal{ID: deps.RabbitMQ, SourceVolume: rabbitGen1}
		require.NoError(t, RollbackCopyUpgrade(ctx, env, j))
		assert.Contains(t, env.Volumes, rabbitGen2)
	})
	t.Run("a temp container that survived leaves the namespace stopped", func(t *testing.T) {
		env := rabbitEnv(t)
		env.FailOn["rm:"+DstContainer] = errors.New("docker is gone")
		j := &deps.MigrationJournal{ID: deps.RabbitMQ, CreatedVolume: rabbitGen2,
			SourceVolume: rabbitGen1, WasRunning: true}
		err := RollbackCopyUpgrade(ctx, env, j)
		require.Error(t, err)
		require.ErrorContains(t, err, "docker is gone")
		require.ErrorContains(t, err, "namespace left stopped")
		assert.Empty(t, env.Reloads(), "nothing was restarted over a temp container that may still exist")
	})
}

// The temp containers carry the dependency's node identity: the environment
// AND the /etc/hosts alias for its host part. Both halves are mandatory — the
// environment alone gives "epmd error for host rabbitmq: nxdomain" and the
// broker never boots (measured) — so they are asserted together, and neither
// may be dropped as a simplification.
func TestCopyUpgradeTempContainersCarryTheNodeIdentity(t *testing.T) {
	env := rabbitEnv(t)
	plan, j := buildCopyPlan(t, env, testSpec(&copyCalls{}, nil))
	require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	for _, name := range []string{SrcContainer, DstContainer} {
		rec, ok := env.RunOpts(name)
		require.True(t, ok, "%s never ran", name)
		assert.Equal(t, map[string]string{"RABBITMQ_NODENAME": "rabbit@rabbitmq"}, rec.Env, "%s", name)
		assert.Equal(t, map[string]string{"rabbitmq": "127.0.0.1"}, rec.HostAlias, "%s", name)
	}
}

// A copy upgrade writes no host scratch file, so its host requirement is
// genuinely 0 — which is exactly why Measured() cannot be "RequiredHostBytes
// is not zero" any more. What it does need is one more copy of the data on the
// filesystem the volumes live on.
func TestCopyPreflightMeasuresOnlyTheVolumeFilesystem(t *testing.T) {
	env := rabbitEnv(t)
	res, vols, ok := CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, rabbitTo,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.True(t, ok, res.Problems)
	assert.Empty(t, res.Problems)
	assert.True(t, res.Measured(), "the space checks ran")
	assert.Equal(t, int64(512<<20), res.DataSizeBytes)
	assert.Equal(t, int64(512<<20)+SpaceMargin, res.RequiredVolumeBytes)
	assert.Zero(t, res.RequiredHostBytes, "no dump, no host file, no host requirement")
	assert.Zero(t, res.RequiredTotalBytes)
	assert.False(t, res.SharedFilesystem)
	assert.Equal(t, CopyVolumes{Source: rabbitGen1, Target: rabbitGen2, TargetGen: 2}, vols)

	env.FreeVolume = 1 << 20
	res, _, ok = CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, rabbitTo,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.True(t, ok)
	assert.Contains(t, joinEN(res.Problems), "not enough free space")
}

// A source volume that is not there means the pin describes data that does not
// exist: there is nothing to copy, and a plan that ran would build an empty
// node and commit it as the migrated one.
func TestCopyPreflightRefusesAMissingSourceVolume(t *testing.T) {
	env := rabbitEnv(t)
	delete(env.Volumes, rabbitGen1)
	res, _, ok := CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, rabbitTo,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.False(t, ok)
	assert.Contains(t, joinEN(res.Problems), rabbitGen1)
}

// A pair the migrator refuses is reported with the migrator's own words, and
// the shared version checks get the first say: they have the accurate message
// for a downgrade and for an unreadable tag.
func TestCopyPreflightReportsTheMigratorsRefusal(t *testing.T) {
	env := rabbitEnv(t)
	refuse := func(deps.Version, deps.Version) (bool, msg.Message) {
		return false, VendorPathProblem("RabbitMQ", "4.1", "4.3", "4.2")
	}
	res, _, ok := CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, "rabbitmq:4.3.5-management", refuse)
	require.False(t, ok)
	joined := joinEN(res.Problems)
	assert.Contains(t, joined, "upgrade to 4.2 first")
	assert.NotContains(t, joined, "update the launcher", "updating the launcher would not help")

	// A downgrade never reaches the migrator: the shared checks word it.
	res, _, ok = CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitTo, rabbitFrom,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.False(t, ok)
	assert.Contains(t, joinEN(res.Problems), "downgrade")
}

// An existing target volume is a confirmation, not a refusal — but building
// the plan without that confirmation must fail, exactly as the PostgreSQL plan
// does.
func TestBuildCopyUpgradeNeedsTheExistingVolumeConfirmed(t *testing.T) {
	env := rabbitEnv(t)
	env.Volumes[rabbitGen2] = map[string]string{}
	env.VolSize[rabbitGen2] = 1 << 20
	pre, _, ok := CopyPreflight(context.Background(), env, deps.RabbitMQ, rabbitFrom, rabbitTo,
		func(deps.Version, deps.Version) (bool, msg.Message) { return true, msg.Message{} })
	require.True(t, ok, pre.Problems)
	pre.OK = len(pre.Problems) == 0
	require.NotNil(t, pre.ExistingTargetVolume)
	assert.Equal(t, rabbitGen2, pre.ExistingTargetVolume.Name)
	assert.Empty(t, pre.ExistingTargetVolume.Version,
		"a dependency whose data carries no version marker must not claim one")

	_, _, err := BuildCopyUpgrade(env, testSpec(&copyCalls{}, nil), rabbitFrom, rabbitTo, PlanOptions{}, pre)
	assert.Contains(t, planProblemsEN(t, err), "already exists")

	plan, j, err := BuildCopyUpgrade(env, testSpec(&copyCalls{}, nil), rabbitFrom, rabbitTo,
		PlanOptions{ReplaceExistingVolume: true}, pre)
	require.NoError(t, err)
	require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	joined := strings.Join(env.Log(), "\n")
	assert.Less(t, strings.Index(joined, "rmvol:"+rabbitGen2), strings.Index(joined, "createvol:"+rabbitGen2))
}

// A plan built from a preflight that failed is a plan that would start work
// the operator was told would not happen.
func TestBuildCopyUpgradeRefusesAFailedPreflight(t *testing.T) {
	env := rabbitEnv(t)
	pre := NewPreflightResult(rabbitFrom, rabbitTo)
	pre.Problems = append(pre.Problems, msg.New("nope"))
	_, _, err := BuildCopyUpgrade(env, testSpec(&copyCalls{}, nil), rabbitFrom, rabbitTo, PlanOptions{}, pre)
	require.ErrorContains(t, err, "nope")
}
