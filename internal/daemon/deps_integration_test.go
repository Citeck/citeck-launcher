//go:build integration

package daemon

// Real-Docker dependency-migration tests: `make test-integration-deps`.
//
// They run the PRODUCTION pieces end to end — the daemon's own depsEnv over a
// real *docker.Client in SERVER mode (bind-dir volumes under a temp
// volumesBase), a real namespace.Runtime as the journal store, the plan from
// migrate.PostgresMigrator and the engine's migrate.Run, wired exactly the way
// handleDependencyMigrate wires them — against real PostgreSQL containers.
//
// What they prove that a fake cannot:
//   - pg_dumpall / psql behavior: the tolerated `role "postgres" already
//     exists` error and the md5 deprecation warnings a real dump replays into
//     a modern server (both captured on stderr and logged as evidence for
//     restoreErrors' tolerance rule);
//   - readiness through the official image's init-time TEMPORARY server, which
//     answers the Unix socket and not TCP — the reason every probe here goes
//     over 127.0.0.1;
//   - real bind-dir volume handling: postgres2 (17's own PGDATA) is only ever
//     READ, postgres3 (18's parent-mount layout) is created next to it;
//   - a failed target leaves the source exactly as it was: no postgres3, no
//     temp containers, no scratch directory — not even the shared parent.
//
// ROOTLESS DOCKER: server mode expects the launcher to be able to READ what a
// container wrote into a data volume (the preflight reads PG_VERSION out of
// postgres2, the rollback deletes postgres3) — true for the root daemon a
// server install runs, and true for rootful Docker. Under ROOTLESS Docker the
// container's uid 999 maps to a subuid the test process does not own, so those
// reads fail with EACCES. Run the target inside a user namespace that maps the
// subuid range instead:
//
//	unshare --user --map-auto --map-root-user make test-integration-deps
//
// requireReadableCluster below fails with that instruction rather than letting
// the permission error surface as an unrelated preflight problem.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

const (
	// itFromImage is the pin the seeded data runs on, itToImage the major it is
	// migrated to. Both are ordinary registry tags: the plan's pull step is a
	// real pull.
	itFromImage = "postgres:17.5"
	itToImage   = "postgres:18"
	// itBadImage pulls but cannot serve PostgreSQL — the sabotage that makes
	// the target fail AFTER the dump and the new volume exist.
	itBadImage = "alpine:3"

	itSeedContainer  = "depsit-seed"
	itCheckContainer = "depsit-check"

	// itDumpFile mirrors the plan's scratch file name (unexported there); the
	// rollback test asserts the dump was real before it sabotaged the target.
	itDumpFile = "dump.sql"

	itSeedRows   = 5000
	itReadyWait  = 3 * time.Minute
	itReadyPoll  = 2 * time.Second
	itTestBudget = 25 * time.Minute
)

// itEnv is one test's world: the production Env (wrapped in an observer), the
// runtime that stores the journal, the namespace's runtime directory and the
// reload the finalize step performs.
type itEnv struct {
	env     *observedEnv
	rt      *namespace.Runtime
	base    string
	reloads *callCounter
}

// callCounter is a mutex-guarded counter; the finalize step runs on the
// engine's goroutine while the test reads it afterwards.
type callCounter struct {
	mu sync.Mutex
	n  int
}

func (c *callCounter) inc()     { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *callCounter) get() int { c.mu.Lock(); defer c.mu.Unlock(); return c.n }

// observedEnv is the production *depsEnv with one addition: it records the
// stderr of every command that produced any. Nothing is intercepted or
// changed — the embedded Env does all the work — and the recording is the only
// way to show what psql actually printed while replaying a real dump, which is
// the evidence restoreErrors' tolerance rule rests on.
type observedEnv struct {
	migrate.Env
	mu   sync.Mutex
	logs []execLog
}

type execLog struct {
	cmd    string
	stderr string
	code   int
}

func (o *observedEnv) Exec(ctx context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	stdout, stderr, exitCode, err = o.Env.Exec(ctx, name, cmd)
	if strings.TrimSpace(stderr) != "" {
		o.mu.Lock()
		o.logs = append(o.logs, execLog{cmd: strings.Join(cmd, " "), stderr: stderr, code: exitCode})
		o.mu.Unlock()
	}
	//nolint:wrapcheck // a transparent observer must hand the Env's error back untouched
	return stdout, stderr, exitCode, err
}

// stderrFor returns the recorded stderr of the last command whose line starts
// with prefix ("pg_dumpall", "psql -h …").
func (o *observedEnv) stderrFor(prefix string) (execLog, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.logs) - 1; i >= 0; i-- {
		if strings.HasPrefix(o.logs[i].cmd, prefix) {
			return o.logs[i], true
		}
	}
	return execLog{}, false
}

// itNamespaceID derives a short, recognizable namespace id from the test name.
// Clamped rather than sliced: a short or punctuated test name must not panic,
// and every leftover on the host has to be recognizable as this test's.
func itNamespaceID(testName string) string {
	name := strings.ToLower(strings.TrimPrefix(testName, "TestIntegration_"))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 8 {
			break
		}
	}
	return "depsit" + b.String()
}

// newITEnv builds the daemon's REAL migration Env for a throwaway namespace:
// a real docker.Client in server mode, a real Runtime as the journal store,
// and a Daemon whose only stub is the reload seam — the finalize step's
// ReloadAndStart is the one thing a harness cannot provide, since a real
// reload needs the whole store/bundle/workspace stack.
func newITEnv(t *testing.T) *itEnv {
	t.Helper()
	nsID := itNamespaceID(t.Name())

	dc, err := docker.NewClient("", nsID)
	require.NoError(t, err, "docker engine unavailable")

	// Unconditional, and scoped to this namespace's own labels: nothing outside
	// them is ever touched.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		dc.PurgeNamespace(ctx, nsID, "")
		_ = dc.Close()
	})

	// Not t.TempDir(): a container writes into these directories as its own
	// uid, so the removal is best-effort — a cleanup failure must not turn a
	// passing test red, it must say what was left behind.
	base, err := os.MkdirTemp("", nsID+"-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(base); rmErr != nil {
			t.Logf("leftover test directory %s: %v", base, rmErr)
		}
	})

	nsCfg := &namespace.Config{
		ID:             nsID,
		Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
		Proxy:          namespace.ProxyProps{Port: 80},
	}
	rt := namespace.NewRuntime(nsCfg, dc, base)
	t.Cleanup(rt.Shutdown)
	// The pin is what the namespace's data runs on; the migration moves it.
	rt.SetDependencyPin(deps.Postgres, itFromImage)

	act := activeNamespace{
		runtime:         rt,
		nsConfig:        nsCfg,
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		systemSecrets:   namespace.SystemSecrets{JWT: "jwt-secret", OIDC: "oidc-secret"},
		volumesBase:     base,
		dockerClient:    dc,
	}
	reloads := &callCounter{}
	d := &Daemon{activeNs: &act}
	// ReloadAndStart refuses an Env whose namespace is not the active one, so
	// the snapshot above is also installed as active.
	d.reloadFn = func() error { reloads.inc(); return nil }
	d.reloadExFn = func(bool, bool, bool) error { reloads.inc(); return nil }

	// The postgres def binds ./postgres/{postgresql,pg_hba}.conf and the init
	// script out of the namespace's runtime directory; a real namespace has
	// them on disk from its last reload, so materialize them the same way
	// generateAndWriteRuntimeFiles does.
	genResp, err := namespace.Generate(nsCfg, act.bundleDef, act.workspaceConfig, act.systemSecrets,
		namespace.GenerateOpts{DependencyPins: rt.DependencyPins()})
	require.NoError(t, err)
	writeRuntimeFiles(base, genResp.Files, nil)

	return &itEnv{env: &observedEnv{Env: d.newDepsEnv(act)}, rt: rt, base: base, reloads: reloads}
}

func (e *itEnv) volumeDir(name string) string { return filepath.Join(e.base, "volumes", name) }

// seed brings up itFromImage on the namespace's own postgres2 bind directory
// and fills it with what a real stand has: two databases, a login role whose
// password is md5-encrypted (the shape a legacy stand carries into a modern
// server), tables, rows and an extension. The container is removed afterwards
// — the migration must start from data alone.
func (e *itEnv) seed(ctx context.Context, t *testing.T) {
	t.Helper()
	started := time.Now()
	require.NoError(t, e.env.PullImage(ctx, itFromImage, func(float64) {}))

	def, err := e.env.GenerateDefFor(deps.Postgres, itFromImage)
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, itSeedContainer, nil)
	require.NoError(t, err)
	e.waitReady(ctx, t, itSeedContainer)

	// md5 on purpose: pg_dumpall writes the hash verbatim and PostgreSQL 18
	// warns about it on restore. That warning is exactly what restoreErrors
	// must NOT treat as a failure.
	e.psql(ctx, t, itSeedContainer, "postgres",
		"SET password_encryption = 'md5'; CREATE ROLE citeck_emodel LOGIN PASSWORD 'citeck_emodel'")
	e.psql(ctx, t, itSeedContainer, "postgres", "CREATE DATABASE citeck_emodel OWNER citeck_emodel")
	e.psql(ctx, t, itSeedContainer, "postgres", "CREATE DATABASE citeck_keycloak")
	e.psql(ctx, t, itSeedContainer, "citeck_emodel", fmt.Sprintf(
		"CREATE EXTENSION IF NOT EXISTS pg_trgm;"+
			"CREATE TABLE ecos_record(id serial primary key, v text);"+
			"INSERT INTO ecos_record(v) SELECT md5(i::text) FROM generate_series(1,%d) i;"+
			"CREATE INDEX ecos_record_v_trgm ON ecos_record USING gin (v gin_trgm_ops)", itSeedRows))
	e.psql(ctx, t, itSeedContainer, "citeck_keycloak",
		"CREATE TABLE realm(id text primary key); INSERT INTO realm VALUES ('ecos-app')")

	require.NoError(t, e.env.StopRemove(ctx, itSeedContainer))
	t.Logf("seed: %s cluster with %d rows ready in %s", itFromImage, itSeedRows, time.Since(started).Round(time.Millisecond))
}

// requireReadableCluster fails early, and with the fix, when the test process
// cannot read what a container wrote — the rootless-Docker uid gap described
// at the top of this file. Without it the same condition surfaces as an
// unrelated preflight problem several minutes later.
func (e *itEnv) requireReadableCluster(ctx context.Context, t *testing.T) {
	t.Helper()
	_, err := e.env.ReadVolumeFile(ctx, deps.PostgresVolumeLegacy, "PG_VERSION")
	if errors.Is(err, fs.ErrPermission) {
		t.Fatalf("cannot read %s/PG_VERSION as this user: %v\n"+
			"Server mode expects the launcher to own what the container wrote (a root daemon, or rootful Docker).\n"+
			"Under rootless Docker run the test in a user namespace that maps the subuid range:\n"+
			"  unshare --user --map-auto --map-root-user make test-integration-deps",
			deps.PostgresVolumeLegacy, err)
	}
	require.NoError(t, err)
}

// waitReady waits for the REAL server, over TCP for the same reason the plan
// does: during first-time init the entrypoint's temporary server answers the
// Unix socket only.
func (e *itEnv) waitReady(ctx context.Context, t *testing.T, container string) {
	t.Helper()
	deadline := time.Now().Add(itReadyWait)
	for {
		running, err := e.env.ContainerRunning(ctx, container)
		require.NoError(t, err)
		require.True(t, running, "container %s exited before it became ready", container)
		if _, _, code, execErr := e.env.Exec(ctx, container,
			[]string{"pg_isready", "-h", "127.0.0.1", "-U", "postgres"}); execErr == nil && code == 0 {
			if _, _, code, execErr := e.env.Exec(ctx, container, itPsqlArgs("postgres", "select 1")); execErr == nil && code == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("container %s did not become ready within %s", container, itReadyWait)
		}
		time.Sleep(itReadyPoll)
	}
}

// itPsqlArgs mirrors the plan's psql invocation: TCP, quiet, unaligned and
// header-less, so stdout is one value per line.
func itPsqlArgs(db, sql string) []string {
	return []string{"psql", "-h", "127.0.0.1", "-U", "postgres", "-d", db, "-q", "-At", "-c", sql}
}

// psql runs a statement and returns its trimmed stdout, failing the test on
// anything but a clean exit.
func (e *itEnv) psql(ctx context.Context, t *testing.T, container, db, sql string) string {
	t.Helper()
	stdout, stderr, code, err := e.env.Exec(ctx, container, itPsqlArgs(db, sql))
	require.NoError(t, err)
	require.Zerof(t, code, "psql %q: %s", sql, stderr)
	return strings.TrimSpace(stdout)
}

// stepTimer records how long every plan step took — the numbers a manual
// upgrade drill needs.
type stepTimer struct {
	mu      sync.Mutex
	order   []string
	elapsed map[string]time.Duration
	current string
	since   time.Time
}

func newStepTimer() *stepTimer {
	return &stepTimer{elapsed: map[string]time.Duration{}, since: time.Now()}
}

func (s *stepTimer) progress(step string, _, _ int, _ float64, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if step == s.current {
		return
	}
	s.closeCurrentLocked()
	s.current = step
	s.order = append(s.order, step)
	s.since = time.Now()
}

func (s *stepTimer) closeCurrentLocked() {
	if s.current != "" {
		s.elapsed[s.current] += time.Since(s.since)
	}
}

func (s *stepTimer) report(t *testing.T) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCurrentLocked()
	s.current = ""
	for _, step := range s.order {
		t.Logf("step %-16s %s", step, s.elapsed[step].Round(time.Millisecond))
	}
	return append([]string(nil), s.order...)
}

// TestIntegration_Postgres17To18 runs the whole production path — the daemon's
// Env, the PostgreSQL plan and the engine — against real containers, and then
// asks the MIGRATED cluster what survived.
func TestIntegration_Postgres17To18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnv(t)
	e.seed(ctx, t)
	e.requireReadableCluster(ctx, t)

	pre := migrate.PostgresMigrator{}.Preflight(ctx, e.env, itFromImage, itToImage)
	require.True(t, pre.OK, "preflight problems: %v", pre.Problems)
	require.Empty(t, pre.Warnings, "a fresh namespace has no leftover target volume")
	assert.False(t, pre.WasRunning, "the harness never starts the namespace")
	t.Logf("preflight: data %d B, free host %d B, free volume %d B", pre.DataSizeBytes, pre.FreeHostBytes, pre.FreeVolumeBytes)

	// Wired exactly as handleDependencyMigrate wires it: plan from the
	// migrator, engine over the namespace Runtime as the journal store.
	plan, journal, err := migrate.PostgresMigrator{}.Plan(ctx, e.env, itFromImage, itToImage, migrate.PlanOptions{})
	require.NoError(t, err)

	timer := newStepTimer()
	started := time.Now()
	runErr := migrate.Run(ctx, e.rt, journal, plan, timer.progress)
	total := time.Since(started)
	steps := timer.report(t)
	require.NoError(t, runErr)
	t.Logf("migration %s → %s took %s", itFromImage, itToImage, total.Round(time.Millisecond))

	assert.Equal(t, []string{
		"stop-namespace", "pull-image", "start-source", "dump", "stop-source",
		"create-volume", "start-target", "restore", "verify", "stop-target",
	}, steps)

	// The evidence the fakes cannot produce: what psql actually printed while
	// replaying a real pg_dumpall script into PostgreSQL 18.
	if log, ok := e.env.stderrFor("psql -h 127.0.0.1 -U postgres -d postgres -q -o /dev/null"); ok {
		t.Logf("restore stderr (exit %d):\n%s", log.code, strings.TrimSpace(log.stderr))
		assert.Contains(t, log.stderr, `role "postgres" already exists`,
			"the one error the restore tolerates is the one a real dump always produces")
	} else {
		t.Log("restore produced no stderr")
	}

	// --- the pin, the journal and the verdict --------------------------------
	assert.Equal(t, itToImage, e.rt.DependencyPins()[deps.Postgres])
	assert.Nil(t, e.rt.MigrationJournal(), "a committed migration clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, deps.PostgresVolumeLegacy, last.OldVolume)
	assert.Equal(t, 1, e.reloads.get(), "finalize reloads the namespace exactly once")

	// --- the volumes ---------------------------------------------------------
	assert.FileExists(t, filepath.Join(e.volumeDir(deps.PostgresVolumeLegacy), "PG_VERSION"),
		"the old cluster is only ever READ")
	pgv, err := os.ReadFile(filepath.Join(e.volumeDir(deps.PostgresVolumeV18), "18", "docker", "PG_VERSION"))
	require.NoError(t, err)
	assert.Equal(t, "18", strings.TrimSpace(string(pgv)))
	assert.NoDirExists(t, depsMigrationDir(e.base), "finalize removes the scratch dir AND its empty parent")

	// --- what the migrated cluster actually holds ----------------------------
	def18, err := e.env.GenerateDefFor(deps.Postgres, itToImage)
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def18, itCheckContainer, nil)
	require.NoError(t, err)
	e.waitReady(ctx, t, itCheckContainer)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()

	assert.True(t, strings.HasPrefix(e.psql(ctx, t, itCheckContainer, "postgres", "SHOW server_version"), "18"),
		"the check container serves the new major")
	assert.Equal(t, "citeck_emodel\nciteck_keycloak\npostgres",
		e.psql(ctx, t, itCheckContainer, "postgres",
			"SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1"))
	assert.Equal(t, "1", e.psql(ctx, t, itCheckContainer, "postgres",
		"SELECT count(*) FROM pg_roles WHERE rolname = 'citeck_emodel'"))
	assert.Equal(t, fmt.Sprint(itSeedRows), e.psql(ctx, t, itCheckContainer, "citeck_emodel",
		"SELECT count(*) FROM ecos_record"))
	assert.Equal(t, "1", e.psql(ctx, t, itCheckContainer, "citeck_keycloak", "SELECT count(*) FROM realm"))
	assert.Equal(t, "1", e.psql(ctx, t, itCheckContainer, "citeck_emodel",
		"SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'"), "extensions come across too")
	assert.Equal(t, "citeck_emodel", e.psql(ctx, t, itCheckContainer, "postgres",
		"SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'citeck_emodel'"),
		"ownership survives")
}

// TestIntegration_RollbackOnBadTarget sabotages the target AFTER the dump and
// the new volume exist — the state a rollback has to undo — and asserts the
// source cluster is left exactly as it was.
func TestIntegration_RollbackOnBadTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	e := newITEnv(t)
	e.seed(ctx, t)
	e.requireReadableCluster(ctx, t)

	before, err := os.ReadFile(filepath.Join(e.volumeDir(deps.PostgresVolumeLegacy), "PG_VERSION"))
	require.NoError(t, err)

	// The sabotage image has to be present: a pull failure would fail the run
	// at a step EARLIER than the one this test is about.
	require.NoError(t, e.env.PullImage(ctx, itBadImage, func(float64) {}))

	plan, journal, err := migrate.PostgresMigrator{}.Plan(ctx, e.env, itFromImage, itToImage, migrate.PlanOptions{})
	require.NoError(t, err)

	// What the world looked like at the moment of failure, so the assertions
	// below cannot pass vacuously.
	var (
		dumpSize        int64
		targetVolume    bool
		journaledVolume string
		dstRunning      bool
	)
	sabotaged := false
	for i := range plan.Steps {
		if plan.Steps[i].ID != "start-target" {
			continue
		}
		sabotaged = true
		plan.Steps[i].Run = func(ctx context.Context, j *migrate.Journal, _ migrate.StepProgress) error {
			dumpSize, _ = e.env.FileSize(filepath.Join(e.env.DumpDir(deps.Postgres), itDumpFile))
			targetVolume, _ = e.env.VolumeExists(ctx, deps.PostgresVolumeV18)
			journaledVolume = j.CreatedVolume

			def, defErr := e.env.GenerateDefFor(deps.Postgres, itToImage)
			if defErr != nil {
				return fmt.Errorf("generate the sabotaged target def: %w", defErr)
			}
			// Pulls, starts, and is not a PostgreSQL server — so the target
			// container EXISTS and runs when the step fails, which is what the
			// rollback has to clean up.
			def.Image = itBadImage
			// PID 1 with no signal handler ignores SIGTERM, so the rollback's
			// stop pays the full graceful-stop grace before the kill — the
			// real path, and the reason this test takes ~30s longer than the
			// happy one.
			def.Cmd = []string{"sleep", "600"}
			if _, runErr := e.env.RunAppDef(ctx, def, migrate.DstContainer, nil); runErr != nil {
				return fmt.Errorf("start the sabotaged target: %w", runErr)
			}
			dstRunning, _ = e.env.ContainerRunning(ctx, migrate.DstContainer)
			return fmt.Errorf("container %s never became a PostgreSQL server", migrate.DstContainer)
		}
	}
	require.True(t, sabotaged, "the plan has no start-target step to sabotage")

	runErr := migrate.Run(ctx, e.rt, journal, plan, nil)
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "step start-target")
	var finalizeErr *migrate.FinalizeError
	assert.NotErrorAs(t, runErr, &finalizeErr, "the migration failed; it did not commit")

	// The failure was LATE: a real dump and a real target volume existed.
	assert.Positive(t, dumpSize, "the dump must exist before the sabotage, or this proves nothing")
	assert.True(t, targetVolume, "the target volume must exist before the sabotage")
	assert.Equal(t, deps.PostgresVolumeV18, journaledVolume, "the volume is journaled before it is created")
	assert.True(t, dstRunning, "the sabotaged target container must be running when the step fails")

	// --- nothing moved -------------------------------------------------------
	assert.Equal(t, itFromImage, e.rt.DependencyPins()[deps.Postgres], "the pin never moves on failure")
	after, err := os.ReadFile(filepath.Join(e.volumeDir(deps.PostgresVolumeLegacy), "PG_VERSION"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the source cluster is still a PostgreSQL 17 data directory")

	// --- nothing was left behind ---------------------------------------------
	assert.NoDirExists(t, e.volumeDir(deps.PostgresVolumeV18))
	assert.NoDirExists(t, depsMigrationDir(e.base), "the scratch dir and its empty parent are gone")
	for _, c := range []string{migrate.SrcContainer, migrate.DstContainer} {
		running, cErr := e.env.ContainerRunning(ctx, c)
		require.NoError(t, cErr)
		assert.False(t, running, "temp container %s survived the rollback", c)
	}
	assert.Zero(t, e.reloads.get(), "a namespace that was not running is not started by a rollback")

	// --- the verdict ---------------------------------------------------------
	assert.Nil(t, e.rt.MigrationJournal(), "a SUCCESSFUL rollback clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.False(t, last.OK())
	assert.Contains(t, last.Error, "start-target")
	assert.NotContains(t, last.Error, "rollback failed")

	// The source cluster is not merely intact on disk, it still SERVES: the
	// whole point of dumping into a new volume is that the old major keeps
	// working after a failed attempt. (A file-by-file comparison would be the
	// wrong check — the dump step runs a real server on this data directory,
	// which legitimately writes WAL, statistics and checkpoints; what must
	// survive is the cluster, not its inode count.)
	def17, err := e.env.GenerateDefFor(deps.Postgres, itFromImage)
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def17, itCheckContainer, nil)
	require.NoError(t, err)
	e.waitReady(ctx, t, itCheckContainer)
	defer func() { assert.NoError(t, e.env.StopRemove(context.Background(), itCheckContainer)) }()
	assert.True(t, strings.HasPrefix(e.psql(ctx, t, itCheckContainer, "postgres", "SHOW server_version"), "17"))
	assert.Equal(t, "citeck_emodel\nciteck_keycloak\npostgres",
		e.psql(ctx, t, itCheckContainer, "postgres",
			"SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1"))
	assert.Equal(t, fmt.Sprint(itSeedRows), e.psql(ctx, t, itCheckContainer, "citeck_emodel",
		"SELECT count(*) FROM ecos_record"))
}
