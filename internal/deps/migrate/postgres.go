package migrate

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// Temp container names and the in-container mount of the scratch directory.
// The names are exported because the daemon's crash recovery and the CLI name
// them in messages, and because the real Env scopes them per namespace.
const (
	SrcContainer = "depsmig-src"
	DstContainer = "depsmig-dst"
	dumpMount    = "/citeck/depsmig"
	dumpFile     = "dump.sql"

	// readyTimeout bounds waiting for a temp server. A first-time init on a
	// slow disk (initdb plus the image's entrypoint) is minutes, not seconds.
	readyTimeout = 5 * time.Minute
	readyPoll    = 2 * time.Second
	// dumpProgressPoll is how often the dump's growth is reported.
	dumpProgressPoll = 2 * time.Second
)

// PostgresMigrator moves a namespace's PostgreSQL data to a new major with a
// logical dump into a NEW volume: pg_dumpall out of a temp container on the
// old data, psql into a temp container on a fresh cluster. The old volume is
// only ever READ, so the rollback is "delete what we made" and the previous
// version keeps working; that is also why an in-place pg_upgrade — which
// rewrites the old cluster — is not what this does.
type PostgresMigrator struct{}

// Preflight checks everything that can be checked before anything is stopped
// or created. It never mutates.
func (PostgresMigrator) Preflight(ctx context.Context, env Env, from, to string) PreflightResult {
	res := PreflightResult{From: from, To: to, WasRunning: env.IsRunning()}
	fromV, toV, problems := postgresVersionProblems(from, to)
	if len(problems) > 0 {
		res.Problems = problems
		return res
	}
	oldLayout := deps.PostgresLayoutFor(fromV.Major)
	newLayout := deps.PostgresLayoutFor(toV.Major)
	// The whole design rests on the new cluster being built NEXT TO the old
	// data: that is what makes the rollback a deletion and the old version
	// still bootable. Two majors that share one volume (any pair below 18)
	// would have this build the new cluster on top of the source — and offer
	// to delete it first, as an existing target volume. Refuse, before the
	// existing-volume warning can name the user's own data.
	if newLayout.Volume == oldLayout.Volume {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"%s → %s keeps the data in volume %s; this migration builds the new cluster in a separate volume and cannot run in place",
			from, to, oldLayout.Volume))
		return res
	}

	res.checkDataVersion(ctx, env, oldLayout, fromV.Major, from)
	res.checkSpace(ctx, env, oldLayout.Volume)
	res.checkExistingTarget(ctx, env, newLayout)

	res.OK = len(res.Problems) == 0
	return res
}

// postgresVersionProblems answers whether this move is one the migrator is
// for. "Significant enough to need a migration" is asked of the REGISTRY's
// descriptor, not restated here: the generator holds a namespace back on
// exactly that rule, so a migrator with its own copy of it could offer to
// migrate something the generator applies silently, or refuse something the
// generator is holding.
func postgresVersionProblems(from, to string) (fromV, toV deps.Version, problems []string) {
	fromV, okFrom := deps.ParseImageVersion(from)
	toV, okTo := deps.ParseImageVersion(to)
	d, registered := deps.Lookup(deps.Postgres)
	switch {
	case !registered:
		problems = append(problems, "PostgreSQL is not a registered dependency")
	case !okFrom:
		problems = append(problems, fmt.Sprintf("cannot read a version out of the current image %q", from))
	case !okTo:
		problems = append(problems, fmt.Sprintf("cannot read a version out of the target image %q", to))
	case !d.IsBreaking(fromV, toV):
		problems = append(problems, fmt.Sprintf(
			"%s → %s is not a major upgrade; it applies on the next start without a migration", from, to))
	case toV.Major < fromV.Major:
		problems = append(problems, fmt.Sprintf(
			"%s → %s is a downgrade; the launcher does not migrate data backwards", from, to))
	}
	return fromV, toV, problems
}

// checkDataVersion reads PG_VERSION out of the data volume. The pin says what
// the namespace THINKS it runs; PG_VERSION is what is actually on disk, and a
// dump taken by the wrong major would fail — or worse, succeed against data
// the pin does not describe.
func (res *PreflightResult) checkDataVersion(ctx context.Context, env Env, layout deps.PostgresLayout, major int, from string) {
	raw, err := env.ReadVolumeFile(ctx, layout.Volume, layout.PGVersionRel)
	if err != nil {
		res.Problems = append(res.Problems, fmt.Sprintf("cannot read %s/%s: %v", layout.Volume, layout.PGVersionRel, err))
		return
	}
	onDisk, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil || onDisk != major {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"PG_VERSION in volume %s is %q but the namespace is pinned to %s",
			layout.Volume, strings.TrimSpace(raw), from))
	}
}

// checkSpace measures the data and both filesystems. Two of them, because on a
// macOS/Windows desktop the dump lands on the host while the new cluster is
// built inside the Docker VM.
func (res *PreflightResult) checkSpace(ctx context.Context, env Env, volume string) {
	size, err := env.VolumeSize(ctx, volume)
	if err != nil {
		res.Problems = append(res.Problems, fmt.Sprintf("cannot measure volume %s: %v", volume, err))
	}
	res.DataSizeBytes = size
	res.RequiredHostBytes = size + SpaceMargin
	res.RequiredVolumeBytes = size + SpaceMargin

	if free, ferr := env.HostFreeBytes(); ferr != nil {
		res.Problems = append(res.Problems, "cannot measure free space on the host: "+ferr.Error())
	} else {
		res.FreeHostBytes = free
		if free < res.RequiredHostBytes {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"not enough free space on the host for the dump: need %s, free %s",
				fsutil.FormatBytes(res.RequiredHostBytes), fsutil.FormatBytes(free)))
		}
	}
	if free, ferr := env.VolumeFreeBytes(ctx, volume); ferr != nil {
		res.Problems = append(res.Problems, "cannot measure free space on the volume filesystem: "+ferr.Error())
	} else {
		res.FreeVolumeBytes = free
		if free < res.RequiredVolumeBytes {
			res.Problems = append(res.Problems, fmt.Sprintf(
				"not enough free space on the volume filesystem for the new cluster: need %s, free %s",
				fsutil.FormatBytes(res.RequiredVolumeBytes), fsutil.FormatBytes(free)))
		}
	}
}

// checkExistingTarget reports an existing target volume as a WARNING, not a
// problem: it is usually the leftover of an attempt that failed, and deleting
// it is exactly what the user wants — once they have seen its size and
// version and said so.
func (res *PreflightResult) checkExistingTarget(ctx context.Context, env Env, layout deps.PostgresLayout) {
	exists, err := env.VolumeExists(ctx, layout.Volume)
	if err != nil {
		res.Problems = append(res.Problems, fmt.Sprintf("cannot check volume %s: %v", layout.Volume, err))
		return
	}
	if !exists {
		return
	}
	ev := ExistingVolume{Name: layout.Volume, Version: "empty"}
	ev.SizeBytes, _ = env.VolumeSize(ctx, layout.Volume)
	if v, rerr := env.ReadVolumeFile(ctx, layout.Volume, layout.PGVersionRel); rerr == nil {
		ev.Version = strings.TrimSpace(v)
	}
	res.ExistingTargetVolume = &ev
	res.Warnings = append(res.Warnings, fmt.Sprintf(
		"volume %s already exists (%s, PostgreSQL %s); it will be DELETED and recreated if you confirm",
		ev.Name, fsutil.FormatBytes(ev.SizeBytes), ev.Version))
}

// pgRun is the state one plan run carries between its steps: the paths it
// computed once and the source inventory the dump step read and the verify
// step compares against. The engine runs steps sequentially on one goroutine,
// which is what makes that hand-off safe.
type pgRun struct {
	env             Env
	from, to        string
	opts            PlanOptions
	oldLayout       deps.PostgresLayout
	newLayout       deps.PostgresLayout
	dumpDir         string
	dumpHostPath    string
	dumpInContainer string
	dumpBind        string
	dataSize        int64

	source pgInventory
}

// Plan builds the major-upgrade plan. It refuses an existing target volume
// unless opts.ReplaceExistingVolume is set (the confirm dialog's checkbox /
// the CLI's --replace-existing).
func (m PostgresMigrator) Plan(ctx context.Context, env Env, from, to string, opts PlanOptions) (*Plan, deps.MigrationJournal, error) {
	pre := m.Preflight(ctx, env, from, to)
	if !pre.OK {
		return nil, deps.MigrationJournal{}, fmt.Errorf("preflight failed: %s", strings.Join(pre.Problems, "; "))
	}
	if pre.ExistingTargetVolume != nil && !opts.ReplaceExistingVolume {
		return nil, deps.MigrationJournal{}, fmt.Errorf(
			"volume %s already exists; confirm replacing it to continue", pre.ExistingTargetVolume.Name)
	}
	fromV, _ := deps.ParseImageVersion(from) // both parsed in the preflight above
	toV, _ := deps.ParseImageVersion(to)
	dumpDir := env.DumpDir(deps.Postgres)
	r := &pgRun{
		env: env, from: from, to: to, opts: opts,
		oldLayout:       deps.PostgresLayoutFor(fromV.Major),
		newLayout:       deps.PostgresLayoutFor(toV.Major),
		dumpDir:         dumpDir,
		dumpHostPath:    filepath.Join(dumpDir, dumpFile),
		dumpInContainer: path.Join(dumpMount, dumpFile),
		dumpBind:        dumpDir + ":" + dumpMount,
		dataSize:        pre.DataSizeBytes,
	}
	j := deps.MigrationJournal{
		ID: deps.Postgres, From: from, To: to, DumpDir: dumpDir,
		WasRunning: env.IsRunning(), StartedAt: time.Now(),
	}
	plan := &Plan{
		Steps: []Step{
			{ID: "stop-namespace", Run: r.stopNamespace},
			{ID: "pull-image", Run: r.pullImage},
			{ID: "start-source", Run: r.startSource},
			{ID: "dump", Run: r.dump},
			{ID: "stop-source", Run: r.stopSource},
			{ID: "create-volume", Run: r.createVolume},
			{ID: "start-target", Run: r.startTarget},
			{ID: "restore", Run: r.restore},
			{ID: "verify", Run: r.verify},
			{ID: "stop-target", Run: r.stopTarget},
		},
		Rollback: func(ctx context.Context, j *deps.MigrationJournal) error { return RollbackPostgres(ctx, env, j) },
		// Only OldVolume: the engine fills the identity (id, from, to) from the
		// journal, which is the single source of truth for what moved where.
		Result: func(*deps.MigrationJournal) deps.MigrationResult {
			return deps.MigrationResult{OldVolume: r.oldLayout.Volume}
		},
		Finalize: r.finalize,
	}
	return plan, j, nil
}

func (r *pgRun) stopNamespace(ctx context.Context, _ *Journal, _ StepProgress) error {
	if !r.env.IsRunning() {
		return nil
	}
	if err := r.env.StopNamespace(ctx); err != nil {
		return fmt.Errorf("stop namespace: %w", err)
	}
	return nil
}

func (r *pgRun) pullImage(ctx context.Context, _ *Journal, p StepProgress) error {
	if err := r.env.PullImage(ctx, r.to, func(pct float64) { p(pct, "pulling "+r.to) }); err != nil {
		return fmt.Errorf("pull %s: %w", r.to, err)
	}
	return nil
}

func (r *pgRun) startSource(ctx context.Context, _ *Journal, p StepProgress) error {
	if err := r.env.EnsureDir(r.dumpDir); err != nil {
		return fmt.Errorf("create %s: %w", r.dumpDir, err)
	}
	return r.startTemp(ctx, r.from, SrcContainer, p)
}

func (r *pgRun) startTarget(ctx context.Context, _ *Journal, p StepProgress) error {
	return r.startTemp(ctx, r.to, DstContainer, p)
}

// startTemp runs one temp container from the namespace's REAL generated def
// for that image — same volume, same PGDATA, same init files — under another
// name. Publishing ports is the Env's business to strip: two temp containers
// and the namespace's own postgres would otherwise fight over one host port.
func (r *pgRun) startTemp(ctx context.Context, image, name string, p StepProgress) error {
	def, err := r.env.GenerateDefFor(deps.Postgres, image)
	if err != nil {
		return fmt.Errorf("generate the %s definition: %w", image, err)
	}
	if _, err := r.env.RunAppDef(ctx, def, name, []string{r.dumpBind}); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return waitReady(ctx, r.env, name, readyTimeout, readyPoll, p)
}

func (r *pgRun) dump(ctx context.Context, _ *Journal, p StepProgress) error {
	// The source inventory is read from the SOURCE SERVER, not from the dump:
	// it is the "before" half of the verify, and reading it here means the
	// comparison is between two live clusters, not between a file and a guess.
	inv, err := readInventory(ctx, r.env, SrcContainer)
	if err != nil {
		return fmt.Errorf("read the source inventory: %w", err)
	}
	r.source = inv

	stop := watchFileGrowth(ctx, r.env, r.dumpHostPath, r.dataSize, dumpProgressPoll, p)
	_, stderr, code, err := r.env.Exec(ctx, SrcContainer,
		[]string{"pg_dumpall", "-h", "127.0.0.1", "-U", "postgres", "-f", r.dumpInContainer})
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

// createVolume makes the new data volume.
//
// Two rules meet here. (1) Write-ahead: the journal claims the volume BEFORE
// it is created, so a crash between the two cannot leave a volume the rollback
// does not know about. (2) The existence check is repeated even though the
// preflight did it, because a volume can appear in between — and if it did,
// and the user has not confirmed replacing it, the step FAILS instead of
// deleting data nobody agreed to lose. The order of the two is deliberate: the
// refusal happens BEFORE anything is journaled, because a journal that claims
// somebody else's volume would have the rollback delete it.
func (r *pgRun) createVolume(ctx context.Context, j *Journal, _ StepProgress) error {
	exists, err := r.env.VolumeExists(ctx, r.newLayout.Volume)
	if err != nil {
		return fmt.Errorf("check volume %s: %w", r.newLayout.Volume, err)
	}
	if exists && !r.opts.ReplaceExistingVolume {
		return fmt.Errorf("volume %s already exists and replacing it was not confirmed", r.newLayout.Volume)
	}
	j.CreatedVolume = r.newLayout.Volume
	if err := j.Persist(); err != nil {
		return fmt.Errorf("journal the new volume: %w", err)
	}
	if exists {
		if err := r.env.RemoveVolume(ctx, r.newLayout.Volume); err != nil {
			return fmt.Errorf("remove the existing volume %s: %w", r.newLayout.Volume, err)
		}
	}
	if err := r.env.CreateVolume(ctx, r.newLayout.Volume); err != nil {
		return fmt.Errorf("create volume %s: %w", r.newLayout.Volume, err)
	}
	return nil
}

// restore replays the dump. psql runs WITHOUT ON_ERROR_STOP (see
// restoreErrors) and reports its errors on stderr even when it exits 0, so
// both the stream and the code have to be judged: an exit code with no ERROR
// line (could not connect, unreadable file) is a failure too.
func (r *pgRun) restore(ctx context.Context, _ *Journal, p StepProgress) error {
	size, _ := r.env.FileSize(r.dumpHostPath)
	p(0, "restoring a "+fsutil.FormatBytes(size)+" dump")
	_, stderr, code, err := r.env.Exec(ctx, DstContainer,
		append(RestoreCommandPrefix(), "-f", r.dumpInContainer))
	if err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	if errs := restoreErrors(stderr); len(errs) > 0 {
		return fmt.Errorf("restore reported %d error(s): %s", len(errs), strings.Join(errs, " | "))
	}
	if code != 0 {
		return fmt.Errorf("psql: exit %d: %s", code, tail(stderr))
	}
	return nil
}

// verify compares the two live clusters and then ANALYZEs the new one: a
// freshly restored cluster has no statistics at all, so without this the first
// hours on the new major run on default estimates and look like a regression
// the migration caused.
func (r *pgRun) verify(ctx context.Context, _ *Journal, p StepProgress) error {
	target, err := readInventory(ctx, r.env, DstContainer)
	if err != nil {
		return fmt.Errorf("read the target inventory: %w", err)
	}
	if d := r.source.diff(target); len(d) > 0 {
		return errors.New(strings.Join(d, "; "))
	}
	p(50, "analyzing")
	_, stderr, code, err := r.env.Exec(ctx, DstContainer,
		[]string{"vacuumdb", "-h", "127.0.0.1", "-U", "postgres", "--all", "--analyze", "-q"})
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
func RollbackPostgres(ctx context.Context, env Env, j *deps.MigrationJournal) error {
	var errs []error
	for _, c := range []string{SrcContainer, DstContainer} {
		if err := env.StopRemove(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", c, err))
		}
	}
	if j.CreatedVolume != "" {
		if err := env.RemoveVolume(ctx, j.CreatedVolume); err != nil {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", j.CreatedVolume, err))
		}
	}
	if err := removeScratch(env, j.DumpDir); err != nil {
		errs = append(errs, err)
	}
	if j.WasRunning {
		if err := env.ReloadAndStart(ctx, true); err != nil {
			errs = append(errs, fmt.Errorf("restart namespace: %w", err))
		}
	}
	return errors.Join(errs...)
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
func waitReady(ctx context.Context, env Env, container string, timeout, poll time.Duration, p StepProgress) error {
	deadline := time.Now().Add(timeout)
	for {
		running, err := env.ContainerRunning(ctx, container)
		if err != nil {
			return fmt.Errorf("check %s: %w", container, err)
		}
		if !running {
			return fmt.Errorf("container %s is not running", container)
		}
		if postgresAnswers(ctx, env, container) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("container %s did not become ready within %s", container, timeout)
		}
		p(0, "waiting for PostgreSQL in "+container)
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", container, ctx.Err())
		case <-time.After(poll):
		}
	}
}

func postgresAnswers(ctx context.Context, env Env, container string) bool {
	if _, _, code, err := env.Exec(ctx, container,
		[]string{"pg_isready", "-h", "127.0.0.1", "-U", "postgres"}); err != nil || code != 0 {
		return false
	}
	_, _, code, err := env.Exec(ctx, container, psqlArgs("postgres", "select 1"))
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
				p(pct, "dumped "+fsutil.FormatBytes(size))
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
