package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/fsutil"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// Temp container names and the in-container mount of the scratch directory.
// The names are exported because the daemon's crash recovery and the CLI name
// them in messages, and because the real Env scopes them per namespace.
const (
	SrcContainer = "depsmig-src"
	DstContainer = "depsmig-dst"
	dumpMount    = "/citeck/depsmig"
	// dumpFile carries the .gz suffix so both the single-hop plan and every
	// rung of a ladder migration write and read a COMPRESSED dump — see
	// DumpScript/RestoreScript in postgres_restore.go.
	dumpFile = "dump.sql.gz"

	// readyTimeout bounds waiting for a temp server. A first-time init on a
	// slow disk (initdb plus the image's entrypoint) is minutes, not seconds.
	readyTimeout = 5 * time.Minute
	readyPoll    = 2 * time.Second
)

// dumpProgressPoll is how often the dump's growth is reported. A var, not a
// const, so a test can shrink it (save, reassign, defer-restore) and observe
// the real runDump call site's progress reports within milliseconds instead
// of waiting on the production cadence — the only way to prove that call
// site still passes 0 rather than merely proving watchFileGrowth draws a
// literal 0 as indeterminate, which a test that calls it directly (bypassing
// runDump entirely) cannot do.
var dumpProgressPoll = 2 * time.Second

// PostgresMigrator moves a namespace's PostgreSQL data to a new major with a
// logical dump into a NEW volume: pg_dumpall out of a temp container on the
// old data, psql into a temp container on a fresh cluster. The old volume is
// only ever READ, so the rollback is "delete what we made" and the previous
// version keeps working; that is also why an in-place pg_upgrade — which
// rewrites the old cluster — is not what this does.
// It is INSTANTIATED PER CLUSTER rather than being a singleton: a namespace can
// hold more than one PostgreSQL (the stand's own database and the observer's),
// and every step below — which pin to read, which volumes to name, which app
// def to build a temp container from, which dump directory to use — is keyed by
// the id this value carries. The registry holds one instance per registered
// PostgreSQL dependency.
type PostgresMigrator struct {
	// ID is the dependency this migrator moves. The zero value is deliberately
	// NOT the main postgres: a migrator built without an id would migrate the
	// wrong cluster's data, so `dep()` refuses it.
	ID deps.ID
}

// dep resolves the descriptor this migrator was built for, refusing an
// unregistered or unset id rather than defaulting to the stand's own database.
func (m PostgresMigrator) dep() (deps.Descriptor, bool) {
	if m.ID == "" {
		return nil, false
	}
	return deps.Lookup(m.ID)
}

// Preflight checks everything that can be checked before anything is stopped
// or created. It never mutates.
//
// Problems and Warnings are ALWAYS non-nil: they cross the API as JSON arrays
// and the web dialog maps over both, so a nil slice — the ordinary happy path
// — would marshal as `null` and crash the confirm screen into the error
// boundary. See NewPreflightResult.
// SupportsPair is asked about the route's ENDPOINTS here, deliberately NOT
// per-hop the way CopyPreflight asks it of every adjacent pair
// (path.Hops()). The two differ because the QUESTION SupportsPair answers is
// a different kind of question for the two plans:
//
//   - For RabbitMQ/ZooKeeper, SupportsPair (via UpgradeSupport) is the
//     VENDOR's own per-hop compatibility table — 4.1 → 4.3 is refused
//     because the vendor never documented that exact jump, whatever the
//     endpoints are. That is inherently a per-hop question, and CopyPreflight
//     asks it of every hop for exactly that reason.
//   - For PostgreSQL, SupportsPair has exactly one rule left — "the move must
//     go forwards" (to.Major > from.Major) — and it is not a vendor
//     restriction on any particular hop at all. The plan is a logical dump
//     (pg_dumpall / psql) that can carry data across ANY forward gap,
//     including a same-major one: nothing about pg_dumpall cares whether the
//     rung it is restoring into is a major bump or a same-major republish.
//     Applying the forwards-only rule per hop would refuse a same-major rung
//     the bundle author deliberately wrote into the ladder (pin 17.5, ladder
//     [17.9, 18.6]: the hop 17.5 → 17.9 shares a major and would fail
//     to.Major > from.Major even though the endpoint move 17.5 → 18.6 is a
//     perfectly good forward major bump the logical dump handles without any
//     extra risk) — which is exactly the "a rung is never skipped, but a
//     rung the author sanctioned is never REFUSED either" balance spec §7
//     insists on. So this checks the ENDPOINTS: is the route as a whole a
//     forward move. Each individual rung is still walked in full by Plan
//     below (nothing here collapses the ladder) — this only decides whether
//     the plan exists at all.
func (m PostgresMigrator) Preflight(ctx context.Context, env Env, route Path) PreflightResult {
	from, to := route.From(), route.To()
	res := NewPreflightResult(from, to)
	res.WasRunning = env.IsRunning()
	fromV, toV, problems := versionProblems(m.ID, from, to)
	if len(problems) > 0 {
		res.Problems = append(res.Problems, problems...)
		return res
	}
	if ok, problem := m.SupportsPair(fromV, toV); !ok {
		res.Problems = append(res.Problems, pairRefusal(problem, from, to))
		return res
	}
	d, _ := m.dep() // registered: versionProblems just asked
	srcVolume, dstVolume, _ := migrationVolumes(d, env.DependencyState(m.ID))
	oldLayout := deps.PostgresLayoutFor(fromV.Major)
	newLayout := deps.PostgresLayoutFor(toV.Major)

	// The source volume's existence first, in the copy plans' words: with it
	// gone (deleted from the Volumes page) there is no data to migrate, and
	// the version read and the free-space measurement below would each fail
	// on it with an error that names the volume without saying why.
	exists, err := env.VolumeExists(ctx, srcVolume)
	switch {
	case err != nil:
		res.Problems = append(res.Problems, VolumeCheckProblem(srcVolume, err))
		return res
	case !exists:
		res.Problems = append(res.Problems,
			msg.New("deps.msg.volume.sourceMissing", "volume", srcVolume, "id", string(m.ID)))
		return res
	}
	res.checkDataVersion(ctx, env, oldLayout, srcVolume, fromV.Major, from)
	res.checkSpace(ctx, env, srcVolume)
	res.checkExistingTarget(ctx, env, dstVolume, newLayout.PGVersionRel)

	res.OK = len(res.Problems) == 0
	return res
}

// SupportsPair reports whether THIS launcher's PostgreSQL plan can move the
// data from one version to the other. It is the migrator's half of the
// question the registry's Migratable() answers for the dependency as a whole:
// the registry says "PostgreSQL is migratable", this says "…and 17 → 19 is a
// pair I know how to do".
//
// There is exactly ONE rule left: the move must go forwards.
//
// It used to have a second — the two majors had to land in DIFFERENT VOLUMES,
// because the new cluster is built next to the old data and the rollback is
// "delete the volume we made". The volume is no longer derived from the
// version at all: it is a persistent generation counter in the pin, and
// migrationVolumes always answers the NEXT generation, so every migration
// lands in a fresh volume by construction. 18 → 19 and 16 → 17 are therefore
// supported like any other forward pair, and the in-place refusal that used to
// describe them has no state left to describe.
//
// The empty problem string is deliberate and is the contract stated in the
// Migrator interface: a pair this refuses is one the shared version checks
// have ALREADY worded better (a downgrade, a same-major move that needs no
// migration at all), and returning a second sentence here would overwrite the
// accurate one with a generic "update the launcher".
func (PostgresMigrator) SupportsPair(from, to deps.Version) (ok bool, problem msg.Message) {
	return to.Major > from.Major, msg.Message{}
}

// checkDataVersion reads PG_VERSION out of the data volume. The pin says what
// the namespace THINKS it runs; PG_VERSION is what is actually on disk, and a
// dump taken by the wrong major would fail — or worse, succeed against data
// the pin does not describe.
func (res *PreflightResult) checkDataVersion(ctx context.Context, env Env, layout deps.PostgresLayout, volume string, major int, from string) {
	raw, err := env.ReadVolumeFile(ctx, volume, layout.PGVersionRel)
	if err != nil {
		res.Problems = append(res.Problems, msg.New("deps.msg.postgres.cannotReadVersionFile",
			"volume", volume, "path", layout.PGVersionRel, "error", err.Error()))
		return
	}
	onDisk, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil || onDisk != major {
		res.Problems = append(res.Problems, msg.New("deps.msg.postgres.dataVersionMismatch",
			"volume", volume, "onDisk", strings.TrimSpace(raw), "image", from))
	}
}

// pgRun is the state one plan run carries between its steps: the paths it
// computed once and the source inventory the dump step read and the verify
// step compares against. The engine runs steps sequentially on one goroutine,
// which is what makes that hand-off safe.
type pgRun struct {
	env Env
	// id is the CLUSTER this run moves. Every def it asks the Env to generate
	// is keyed by it: a namespace can hold more than one PostgreSQL, and a
	// temp container built from the wrong one would mount the wrong volume.
	id deps.ID
	// creds is who this cluster is talked to AS — read off its own generated
	// def, because the clusters do not share a superuser (see pgCreds).
	creds pgCreds
	path  Path
	opts  PlanOptions
	// fromGen is the generation the SOURCE cluster lives in — always the
	// namespace's own pin, read once. toGen is the generation the FINAL
	// cluster lands in; every intermediate rung lives in scratchVolume
	// instead and consumes no generation at all.
	fromGen, toGen int
	srcVolume      string
	dstVolume      string
	// scratchVolume is the ONE reusable intermediate cluster a multi-rung walk
	// climbs through — "" for a single-hop migration, where it is never
	// created at all.
	scratchVolume string
	dumpDir       string
	// dumpHostPath/dumpInContainer/dumpBind name ONE dump file, reused by
	// every rung: it is written, restored and removed before the next rung's
	// dump is ever taken (see restoreAt), so there is never more than one on
	// disk and nothing is gained by giving each rung's dump a distinct name.
	// Single-hop tests pin this exact path
	// (TestRestoreRunsTheExportedCommandPrefix), which is the other reason it
	// stays fixed rather than becoming per-rung.
	dumpHostPath    string
	dumpInContainer string
	dumpBind        string

	source pgInventory
}

// isLastRung reports whether rung i (0-based over path.Rungs()) is the top of
// the ladder — the one that lands in the FINAL generation rather than the
// reusable scratch volume.
func (r *pgRun) isLastRung(i int) bool {
	return i == len(r.path.Rungs())-1
}

// Plan builds the upgrade plan, walking every rung of route in ONE migration
// with one journal and one generation increment. It refuses an existing
// target volume unless opts.ReplaceExistingVolume is set (the confirm
// dialog's checkbox / the CLI's --replace-existing).
//
// At route.Len() == 2 (the ordinary single-hop case) this produces exactly
// today's 10 steps, step for step: the loop below runs once, with i == 0 the
// only and therefore the LAST rung, so every "not last" branch is simply never
// taken.
func (m PostgresMigrator) Plan(ctx context.Context, env Env, route Path, opts PlanOptions) (*Plan, deps.MigrationJournal, error) {
	pre := m.Preflight(ctx, env, route)
	if !pre.OK {
		return nil, deps.MigrationJournal{}, refusePlan(pre.Problems...)
	}
	if pre.ExistingTargetVolume != nil && !opts.ReplaceExistingVolume {
		return nil, deps.MigrationJournal{}, refusePlan(existingVolumeRefusal(pre.ExistingTargetVolume.Name))
	}
	d, ok := m.dep()
	if !ok {
		return nil, deps.MigrationJournal{}, refusePlan(NotRegisteredProblem(m.ID))
	}
	// The pin is read ONCE, here, and both volume names and the generation the
	// commit will move to are derived from that one reading — the journal then
	// carries them, so nothing later has to re-derive them from a world the
	// migration is rewriting. The scratch volume is a function of toGen alone
	// (see deps.ScratchVolumeName) and does not vary with the ladder's length.
	state := env.DependencyState(m.ID)
	srcVolume, dstVolume, toGen := migrationVolumes(d, state)
	dumpDir := env.DumpDir(m.ID)
	// The creds come from the namespace's OWN def for this cluster: the temp
	// containers are built from it, so reading them anywhere else would let the
	// plan address a server it just started under a role that does not exist
	// there. A def that cannot be generated leaves the image's defaults, and
	// the very first container step then fails with the real reason.
	creds := defaultPgCreds
	if def, defErr := env.GenerateDefFor(m.ID, state); defErr == nil {
		creds = pgCredsFromDef(def)
	} else {
		slog.Warn("Could not read the cluster's credentials from its definition; "+
			"assuming the image defaults", "dependency", m.ID, "err", defErr)
	}
	r := &pgRun{
		env: env, id: m.ID, creds: creds, path: route, opts: opts,
		fromGen:         state.Gen(),
		toGen:           toGen,
		srcVolume:       srcVolume,
		dstVolume:       dstVolume,
		scratchVolume:   deps.ScratchVolumeName(d, toGen),
		dumpDir:         dumpDir,
		dumpHostPath:    filepath.Join(dumpDir, dumpFile),
		dumpInContainer: path.Join(dumpMount, dumpFile),
		dumpBind:        dumpDir + ":" + dumpMount,
	}
	j := deps.MigrationJournal{
		ID: m.ID, From: route.From(), To: route.To(), DumpDir: dumpDir,
		ToVolumeGen: toGen, SourceVolume: srcVolume,
		WasRunning: env.IsRunning(), StartedAt: time.Now(),
	}
	steps := []Step{
		{ID: "stop-namespace", Run: r.stopNamespace},
		{ID: "pull-image", Run: r.pullImages},
		{ID: "start-source", Run: r.startSource},
		{ID: "dump", Run: r.dumpFromSource},
		{ID: "stop-source", Run: r.stopSource},
	}
	rungs := route.Rungs()
	for i, image := range rungs {
		last := r.isLastRung(i)
		steps = append(steps,
			Step{ID: "create-volume", Run: r.createVolumeFor(i)},
			Step{ID: "start-target", Run: r.startTargetAt(i, image)},
			Step{ID: "restore", Run: r.restoreAt(i)},
		)
		if last {
			steps = append(steps,
				Step{ID: "verify", Run: r.verify},
				Step{ID: "stop-target", Run: r.stopTarget},
			)
			break
		}
		// The container running rung i is the source of rung i+1's dump: it is
		// already up and already holds the data. Dumping from it rather than
		// starting a second container is what keeps the peak at one cluster.
		steps = append(steps,
			Step{ID: "dump", Run: r.dumpFromCurrent},
			Step{ID: "stop-target", Run: r.stopTarget},
			// The cluster this rung ran on goes as soon as it has been dumped,
			// before the next create-volume recreates it: see discardScratch.
			Step{ID: "create-volume", Run: r.discardScratch},
		)
	}
	plan := &Plan{
		Diagnose: tempContainerDiagnostics(env),
		Steps:    steps,
		Rollback: func(ctx context.Context, j *deps.MigrationJournal) error { return RollbackPostgres(ctx, env, j) },
		// Only OldVolume, and it is read from the JOURNAL rather than from the
		// run: the engine fills the identity (id, from, to) from there too,
		// which is the single source of truth for what moved where.
		Result: func(j *deps.MigrationJournal) deps.MigrationResult {
			return deps.MigrationResult{OldVolume: j.SourceVolume}
		},
		Finalize: r.finalize,
	}
	return plan, j, nil
}

func (r *pgRun) stopNamespace(ctx context.Context, _ *Journal, _ StepProgress) error {
	return stopNamespaceStep(ctx, r.env)
}

// pullImages pulls EVERY rung before anything irreversible happens: an
// unreachable registry then costs a stopped namespace and nothing else,
// whatever rung it is on. At a single hop this pulls exactly r.path.To(), as
// pullImage always did.
func (r *pgRun) pullImages(ctx context.Context, _ *Journal, p StepProgress) error {
	for _, image := range r.path.Rungs() {
		if err := pullImageStep(ctx, r.env, image, p); err != nil {
			return err
		}
	}
	return nil
}

// startSource runs the OLD image on the generation the pin names — the
// namespace's own data volume, which this plan reads and never writes: it is
// what pg_dumpall is pointed at, and it is the one place the two plans differ
// in kind (the copy-upgrade plan never starts anything on the source at all).
func (r *pgRun) startSource(ctx context.Context, _ *Journal, p StepProgress) error {
	if err := r.env.EnsureDir(r.dumpDir); err != nil {
		return fmt.Errorf("create %s: %w", r.dumpDir, err)
	}
	return r.startTemp(ctx, r.path.From(), r.fromGen, SrcContainer, p)
}

// startTargetAt starts rung i's container: on the FINAL generation for the
// top rung, on the reusable scratch volume for every intermediate one.
func (r *pgRun) startTargetAt(i int, image string) func(context.Context, *Journal, StepProgress) error {
	last := r.isLastRung(i)
	return func(ctx context.Context, _ *Journal, p StepProgress) error {
		if last {
			return r.startTemp(ctx, image, r.toGen, DstContainer, p)
		}
		return r.startTempOnVolume(ctx, image, r.scratchVolume, DstContainer, p)
	}
}

// startTemp runs one temp container from the namespace's REAL generated def
// for that image and that volume generation — same mount path, same PGDATA,
// same init files — under another name. The generation is what decides which
// volume the container lands on, so it is an argument and never a default:
// the source container must see the old cluster and the target container must
// see the volume the plan just created. Publishing ports is the Env's business
// to strip: two temp containers and the namespace's own postgres would
// otherwise fight over one host port.
func (r *pgRun) startTemp(ctx context.Context, image string, gen int, name string, p StepProgress) error {
	def, err := r.env.GenerateDefFor(r.id, deps.DependencyState{Image: image, VolumeGen: gen})
	if err != nil {
		return fmt.Errorf("generate the %s definition: %w", image, err)
	}
	return r.runTemp(ctx, def, name, p)
}

// startTempOnVolume is startTemp's counterpart for an intermediate rung: the
// container mounts VOLUME — the reusable scratch cluster — which cannot be
// expressed as a generation at all, so it goes through GenerateDefForVolume
// rather than GenerateDefFor. This is the ONLY place this plan asks for a
// volume that is not the namespace's source or its final target.
//
// The refusal below is load-bearing, not decorative. VolumeGen is left unset
// on the DependencyState passed to GenerateDefForVolume (so Gen() clamps it
// to 1), which means GenerateDefForVolume's own "ordinary" mount — the one
// its retarget/verify guards compare against — is generation 1's volume. On a
// namespace that has never migrated, generation 1 IS the source volume. If
// VOLUME here were ever the source too (a caller bug: passing r.srcVolume
// instead of r.scratchVolume), mountVolume would equal ordinary, the whole
// retarget branch in GenerateDefForVolume — and both of its post-conditions —
// would be skipped as a no-op, and the trailing mountsVolume(a, mountVolume)
// check would pass trivially, because the def already mounts the source by
// construction. Those guards were built to catch a misbehaving GENERATOR;
// they cannot catch a PLAN that names the source on purpose. So this function
// checks the one thing they structurally cannot: refuse outright if asked to
// mount the source, before GenerateDefForVolume is ever called.
func (r *pgRun) startTempOnVolume(ctx context.Context, image, volume, name string, p StepProgress) error {
	if volume != "" && volume == r.srcVolume {
		return fmt.Errorf(
			"refusing to start %s for an intermediate rung on %q: that is the SOURCE volume — "+
				"an intermediate rung must run on the scratch volume, never on the data this plan promises only to read",
			name, volume)
	}
	def, err := r.env.GenerateDefForVolume(r.id, deps.DependencyState{Image: image}, volume)
	if err != nil {
		return fmt.Errorf("generate the %s definition: %w", image, err)
	}
	return r.runTemp(ctx, def, name, p)
}

func (r *pgRun) runTemp(ctx context.Context, def appdef.ApplicationDef, name string, p StepProgress) error {
	if _, err := r.env.RunAppDef(ctx, def, deps.TempContainerOpts{
		Name: name, ExtraBinds: []string{r.dumpBind},
	}); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return waitReady(ctx, r.env, name, r.creds, readyTimeout, readyPoll, p)
}

// dumpFromSource is the bottom dump: it also captures the "before" inventory,
// since the SOURCE server is read here and nowhere else in the plan.
func (r *pgRun) dumpFromSource(ctx context.Context, _ *Journal, p StepProgress) error {
	// The source inventory is read from the SOURCE SERVER, not from the dump:
	// it is the "before" half of the verify, and reading it here means the
	// comparison is between two live clusters, not between a file and a guess.
	inv, err := readInventory(ctx, r.env, SrcContainer, r.creds)
	if err != nil {
		return fmt.Errorf("read the source inventory: %w", err)
	}
	r.source = inv
	return r.runDump(ctx, SrcContainer, p)
}

// dumpFromCurrent dumps the node an intermediate rung just raised the data
// to, so the NEXT rung has something to restore. The container running rung i
// is already up and already holds the data — dumping from it is what keeps
// the peak at one cluster instead of starting a second one.
func (r *pgRun) dumpFromCurrent(ctx context.Context, _ *Journal, p StepProgress) error {
	return r.runDump(ctx, DstContainer, p)
}

func (r *pgRun) runDump(ctx context.Context, container string, p StepProgress) error {
	// 0, not the cluster's data size: the file this watches is compressed, so
	// its size is not a fraction of the cluster's. A percentage computed from
	// it creeps to ~15% and then jumps to done, which reads as a stall; both
	// renderers already draw 0 as indeterminate — the same choice the
	// volume-copy step makes.
	stop := watchFileGrowth(ctx, r.env, r.dumpHostPath, 0, dumpProgressPoll, p)
	_, stderr, code, err := r.env.Exec(ctx, container, DumpScript(r.creds, r.dumpInContainer))
	stop()
	if err != nil {
		return fmt.Errorf("pg_dumpall: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("pg_dumpall: exit %d: %s", code, tail(stderr))
	}
	return nil
}

func (r *pgRun) stopSource(ctx context.Context, _ *Journal, _ StepProgress) error {
	if err := r.env.StopRemove(ctx, SrcContainer); err != nil {
		return fmt.Errorf("remove %s: %w", SrcContainer, err)
	}
	return nil
}

func (r *pgRun) stopTarget(ctx context.Context, _ *Journal, _ StepProgress) error {
	if err := r.env.StopRemove(ctx, DstContainer); err != nil {
		return fmt.Errorf("remove %s: %w", DstContainer, err)
	}
	return nil
}

// createVolumeFor makes the volume rung i is restored into: the FINAL target
// (write-ahead discipline shared with the other plan, via createTargetVolume)
// for the top rung, the reusable scratch cluster for every intermediate one.
func (r *pgRun) createVolumeFor(i int) func(context.Context, *Journal, StepProgress) error {
	last := r.isLastRung(i)
	return func(ctx context.Context, j *Journal, _ StepProgress) error {
		if last {
			return createTargetVolume(ctx, r.env, j, r.dstVolume, r.opts.ReplaceExistingVolume)
		}
		return createScratchVolume(ctx, r.env, j, r.scratchVolume)
	}
}

// createScratchVolume makes (or remakes) the ONE intermediate cluster a
// multi-rung walk reuses, journaling it as ScratchVolume — not CreatedVolume,
// which names the FINAL cluster only — before creating it, exactly as
// createTargetVolume journals CreatedVolume before creating the target. It
// always replaces whatever is already there: unlike an ordinary target
// volume, the scratch volume never holds anything an operator asked to keep,
// so its reuse needs no confirmation.
func createScratchVolume(ctx context.Context, env Env, j *Journal, volume string) error {
	exists, err := env.VolumeExists(ctx, volume)
	if err != nil {
		return fmt.Errorf("check volume %s: %w", volume, err)
	}
	j.ScratchVolume = volume
	if err := j.Persist(); err != nil {
		return fmt.Errorf("journal the scratch volume: %w", err)
	}
	if exists {
		if err := env.RemoveVolume(ctx, volume); err != nil {
			return fmt.Errorf("remove the existing scratch volume %s: %w", volume, err)
		}
	}
	if err := env.CreateVolume(ctx, volume); err != nil {
		return fmt.Errorf("create volume %s: %w", volume, err)
	}
	return nil
}

// discardScratch removes the reusable intermediate cluster once its data has
// been dumped out of it — before the next create-volume recreates it under
// the same name. Removing it HERE, rather than at the end of the migration,
// is what keeps the peak at one cluster whatever the ladder's length: two
// clusters would otherwise coexist for the rest of the walk.
func (r *pgRun) discardScratch(ctx context.Context, _ *Journal, _ StepProgress) error {
	if err := r.env.RemoveVolume(ctx, r.scratchVolume); err != nil {
		return fmt.Errorf("remove scratch volume %s: %w", r.scratchVolume, err)
	}
	return nil
}

// restoreAt replays the ONE dump file into rung i's container. psql runs
// WITHOUT ON_ERROR_STOP (see restoreErrors) and reports its errors on stderr
// even when it exits 0, so both the stream and the code have to be judged: an
// exit code with no ERROR line (could not connect, unreadable file) is a
// failure too.
//
// For every rung but the last, it also removes the dump it just restored, in
// the TAIL of this same step. The dump is dead the instant the restore
// succeeds: any failure from here on rolls back to the untouched SOURCE, so
// no dump is ever needed twice, and removing it here — before the next dump
// is taken — is what keeps the peak at one dump plus one cluster. The top
// rung's dump is left for Finalize's removeScratch to clear along with the
// rest of the scratch directory, exactly as the single-hop plan has always
// done, which is what keeps a single-hop migration's call log unchanged.
func (r *pgRun) restoreAt(i int) func(context.Context, *Journal, StepProgress) error {
	last := r.isLastRung(i)
	return func(ctx context.Context, _ *Journal, p StepProgress) error {
		size, _ := r.env.FileSize(r.dumpHostPath)
		p(0, msg.New("deps.msg.progress.restoring", "size", fsutil.FormatBytes(size)))
		_, stderr, code, err := r.env.Exec(ctx, DstContainer, RestoreScript(r.creds, r.dumpInContainer))
		if err != nil {
			return fmt.Errorf("psql: %w", err)
		}
		if errs := restoreErrors(stderr, r.creds); len(errs) > 0 {
			return fmt.Errorf("restore reported %d error(s): %s", len(errs), strings.Join(errs, " | "))
		}
		if code != 0 {
			return fmt.Errorf("psql: exit %d: %s", code, tail(stderr))
		}
		if last {
			return nil
		}
		if err := r.env.RemoveDir(r.dumpHostPath); err != nil {
			return fmt.Errorf("remove dump %s: %w", r.dumpHostPath, err)
		}
		return nil
	}
}

// verify compares the two live clusters and then ANALYZEs the new one: a
// freshly restored cluster has no statistics at all, so without this the first
// hours on the new major run on default estimates and look like a regression
// the migration caused.
func (r *pgRun) verify(ctx context.Context, _ *Journal, p StepProgress) error {
	target, err := readInventory(ctx, r.env, DstContainer, r.creds)
	if err != nil {
		return fmt.Errorf("read the target inventory: %w", err)
	}
	if d := r.source.diff(target); len(d) > 0 {
		return errors.New(strings.Join(d, "; "))
	}
	p(50, msg.New("deps.msg.progress.analyzing"))
	_, stderr, code, err := r.env.Exec(ctx, DstContainer,
		[]string{"vacuumdb", "-h", "127.0.0.1", "-U", r.creds.User, "--all", "--analyze", "-q"})
	if err != nil {
		return fmt.Errorf("vacuumdb: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("vacuumdb: exit %d: %s", code, tail(stderr))
	}
	return nil
}

// finalize runs AFTER the commit, so its failures are warnings: the data has
// moved and the pin says so.
func (r *pgRun) finalize(ctx context.Context, j *deps.MigrationJournal) error {
	if err := removeScratch(r.env, j.DumpDir); err != nil {
		return err
	}
	if err := r.env.ReloadAndStart(ctx, j.WasRunning); err != nil {
		return fmt.Errorf("reload the namespace: %w", err)
	}
	return nil
}

// removeScratch deletes a migration's scratch directory and then the shared
// parent — but only if that parent is empty, since another dependency's
// migration may still be using it.
func removeScratch(env Env, dumpDir string) error {
	if dumpDir == "" {
		return nil
	}
	if err := env.RemoveDir(dumpDir); err != nil {
		return fmt.Errorf("remove %s: %w", dumpDir, err)
	}
	if parent := filepath.Dir(dumpDir); parent != "" && parent != dumpDir {
		if err := env.RemoveDirIfEmpty(parent); err != nil {
			return fmt.Errorf("remove %s: %w", parent, err)
		}
	}
	return nil
}

// RollbackPostgres undoes whatever the journal says was done, and nothing
// else: the old data volume is never named in the journal, so it can never be
// removed here. Idempotent — every removal treats not-found as success — which
// is what lets the daemon's crash recovery run it against a journal whose work
// may have been undone already.
//
// It reports every failure it hit rather than stopping at the first: a
// half-done rollback is what the engine keeps the journal open for, and the
// operator needs to know which halves.
//
// The namespace is handed back RUNNING only when the cleanup left nothing
// behind. depsmig-src mounts the namespace's own data volume read-write, so a
// restart over a temp container that could not be removed would put a SECOND
// postmaster on the user's only copy of the data — the postmaster.pid
// interlock does not hold across PID/IPC namespaces, so nothing would stop it.
// This is the same rule the boot recovery already follows (it hands the
// namespace back running only when the rollback succeeded); a stopped
// namespace with an open journal is recoverable, a corrupted cluster is not.
func RollbackPostgres(ctx context.Context, env Env, j *deps.MigrationJournal) error {
	var errs []error
	for _, c := range []string{SrcContainer, DstContainer} {
		if err := env.StopRemove(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", c, err))
		}
	}
	for _, v := range volumesToRemove(j) {
		if err := env.RemoveVolume(ctx, v); err != nil {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", v, err))
		}
	}
	if err := removeScratch(env, j.DumpDir); err != nil {
		errs = append(errs, err)
	}
	if j.WasRunning {
		switch {
		case len(errs) > 0:
			errs = append(errs, errors.New(
				"namespace left stopped: the rollback could not remove its temp containers"))
		default:
			if err := env.ReloadAndStart(ctx, true); err != nil {
				errs = append(errs, fmt.Errorf("restart namespace: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

// volumesToRemove is every volume this migration created: the FINAL target,
// plus the reusable intermediate a multi-rung walk left behind. A journal
// written by an older launcher — before ScratchVolume existed — has that
// field empty, so the union is also what keeps this correct for a journal
// this build did not write: it simply contributes nothing.
func volumesToRemove(j *deps.MigrationJournal) []string {
	out := make([]string, 0, 2)
	for _, v := range []string{j.CreatedVolume, j.ScratchVolume} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// waitReady waits for the REAL server in container.
//
// Readiness is TWO probes over TCP, not one over the socket: the official
// image's entrypoint runs a TEMPORARY server during first-time init that
// listens on the Unix socket only, so a socket pg_isready succeeds there and
// then starts failing when that server is torn down. 127.0.0.1 is answered
// only by the final server (the embedded pg_hba.conf has
// `host all all 127.0.0.1/32 trust`), and the `select 1` behind pg_isready is
// what proves the server accepts connections rather than merely listening.
func waitReady(ctx context.Context, env Env, container string, c pgCreds, timeout, poll time.Duration, p StepProgress) error {
	deadline := time.Now().Add(timeout)
	for {
		running, err := env.ContainerRunning(ctx, container)
		if err != nil {
			return fmt.Errorf("check %s: %w", container, err)
		}
		if !running {
			return fmt.Errorf("container %s is not running", container)
		}
		if postgresAnswers(ctx, env, container, c) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("container %s did not become ready within %s", container, timeout)
		}
		p(0, progressWaiting("PostgreSQL", container))
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", container, ctx.Err())
		case <-time.After(poll):
		}
	}
}

func postgresAnswers(ctx context.Context, env Env, container string, c pgCreds) bool {
	if _, _, code, err := env.Exec(ctx, container,
		[]string{"pg_isready", "-h", "127.0.0.1", "-U", c.User}); err != nil || code != 0 {
		return false
	}
	_, _, code, err := env.Exec(ctx, container, psqlArgs(c, c.DB, "select 1"))
	return err == nil && code == 0
}

// watchFileGrowth reports the dump's progress from the size of the file it is
// being written to — the only measure available, since pg_dumpall says nothing
// until it is done. The returned stop function WAITS for the reporter to
// return, so a finished step's progress callback is never called afterwards.
func watchFileGrowth(ctx context.Context, env Env, file string, expected int64, every time.Duration, p StepProgress) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				size, err := env.FileSize(file)
				if err != nil {
					continue
				}
				pct := 0.0
				if expected > 0 {
					// A dump is normally smaller than the cluster it came from,
					// but it does not have to be — never report past 99%.
					pct = min(99, float64(size)/float64(expected)*100)
				}
				p(pct, msg.New("deps.msg.progress.dumped", "size", fsutil.FormatBytes(size)))
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// tail keeps the last few lines of a tool's output: enough for the reason,
// short enough for an error string and a toast.
func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " | ")
}
