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
// With a ROOTFUL daemon a docker-group user hits the same EACCES (uid 999 is a
// real uid there, PGDATA is 0700) and a user namespace does NOT help — that
// case is `sudo make test-integration-deps`. requireReadableCluster below
// fails with both instructions rather than letting the permission error
// surface as an unrelated preflight problem.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	//
	// itToImage must be the image the GENERATOR would emit for that major, not
	// merely an image of it. GenerateDefFor refuses a def whose image is not
	// the one it was asked for, and the pin gate only emits a requested version
	// verbatim while the move is BREAKING — asking for "postgres:18" against a
	// generator whose fallback is "postgres:18.6" is not breaking (same major),
	// so the gate answers the bundle's candidate and every temp container is
	// refused. It is a floating-vs-concrete tag question, and R3 made the
	// generator concrete: keep this equal to generator_infra.go's postgres
	// fallback.
	itFromImage = "postgres:17.5"
	itToImage   = "postgres:18.6"
	// itBadImage pulls but cannot serve PostgreSQL — the sabotage that makes
	// the target fail AFTER the dump and the new volume exist.
	itBadImage = "alpine:3"

	itSeedContainer  = "depsit-seed"
	itCheckContainer = "depsit-check"

	itSeedRows   = 5000
	itReadyWait  = 3 * time.Minute
	itReadyPoll  = 2 * time.Second
	itTestBudget = 25 * time.Minute
)

// itEnv is one test's world: the production Env (wrapped in an observer), the
// runtime that stores the journal, the namespace's runtime directory and the
// reload the finalize step performs.
//
// d and mux are the daemon the Env was built from and its route table. A plan
// is driven directly (migrate.Run, as handleDependencyMigrate does), but the
// ROLLBACK is not a plan: it is three steps in a route, behind the long-op
// lock, the journal blocker and the namespace-status gate. Driving it through
// the real mux is what makes those gates part of what the test proves rather
// than something it works around.
type itEnv struct {
	env     *observedEnv
	rt      *namespace.Runtime
	dc      *docker.Client
	d       *Daemon
	mux     *http.ServeMux
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

// recordedCommands lists the command lines that produced stderr — what a
// failed stderrFor lookup has to show to be diagnosable.
func (o *observedEnv) recordedCommands() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.logs))
	for _, l := range o.logs {
		out = append(out, l.cmd)
	}
	return out
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
//
// The clamp alone is not enough to keep it UNIQUE — two tests whose names
// share their first eight alphanumerics would get one id, and this id names
// the Docker network, the container labels, the data volumes and the temp
// directory a test purges in its cleanup. That is not a flaky assertion, it is
// one test deleting another's containers mid-run, so the FULL name is folded
// into a short suffix: recognizable prefix, collision-free tail.
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
	sum := sha256.Sum256([]byte(testName))
	return "depsit" + b.String() + hex.EncodeToString(sum[:2])
}

// The id is what scopes EVERY destructive act in these tests — the container
// labels a cleanup purges, the data volumes, the temp directory. Two tests
// sharing one id is one test purging another's containers while it runs, so
// the property is checked here rather than left to the naming of future test
// functions. It needs no Docker: run it alone with
// `go test -tags integration ./internal/daemon/ -run TestIntegration_NamespaceIDsAreDistinct`.
func TestIntegration_NamespaceIDsAreDistinct(t *testing.T) {
	names := []string{
		// Every real test in this build tag, so the property is checked for
		// the set that actually runs and not only for the two it started with.
		"TestIntegration_Postgres17To18",
		"TestIntegration_RollbackOnBadTarget",
		"TestIntegration_Rabbit41To42",
		"TestIntegration_CopyPreservesOwnership",
		"TestIntegration_Zookeeper38To39",
		"TestIntegration_CopyRollbackOnBadTarget",
		"TestIntegration_TempRabbitDoesNotAnswerOnTheNamespaceNetwork",
		"TestIntegration_RollbackAfterPostgres17To18",
		"TestIntegration_RollbackRefusedWhenTheRetainedVolumeIsGone",
		// The collision the clamp alone allowed: identical for eight
		// alphanumerics, and both plausible names for real tests.
		"TestIntegration_RollbackOnAMissingVolume",
		"TestIntegration_RollbackOnAFailedRestore",
		"Test", // no suffix at all must not panic
	}
	seen := map[string]string{}
	for _, name := range names {
		id := itNamespaceID(name)
		assert.True(t, strings.HasPrefix(id, "depsit"), "%s → %q must stay recognizable", name, id)
		if prev, dup := seen[id]; dup {
			t.Errorf("%s and %s share the namespace id %q — each would purge the other's containers", prev, name, id)
		}
		seen[id] = name
	}
}

// itDumpBytes is the total size of whatever the plan wrote into its scratch
// directory, which is what "the dump was real" means here. It measures the
// DIRECTORY rather than naming a file: the plan's scratch file name is its own
// business (unexported), and a copy of that name here would be a second
// definition of it that keeps compiling after the plan changes — the test
// would then measure a file that does not exist, read 0, and report the dump
// as empty. Server mode only, which is the mode this test runs in: the scratch
// directory is a host path there.
func itDumpBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

// newITEnv is newITEnvFor for the PostgreSQL pair the first two tests use.
func newITEnv(t *testing.T) *itEnv {
	t.Helper()
	return newITEnvFor(t, deps.Postgres, itFromImage)
}

// newITEnvFor builds the daemon's REAL migration Env for a throwaway namespace
// whose dependency dep is pinned to fromImage: a real docker.Client in server
// mode, a real Runtime as the journal store, and a Daemon whose only stub is
// the reload seam — the finalize step's ReloadAndStart is the one thing a
// harness cannot provide, since a real reload needs the whole
// store/bundle/workspace stack.
//
// The pin is passed in rather than fixed because it decides two things at
// once: which volume the plan reads (the generation counter hangs off the
// state) and which image the generator is allowed to emit — GenerateDefFor
// refuses a def whose image or volume is not the one it was asked for, and
// that guard only holds while the pin says what this namespace's data really
// runs on.
func newITEnvFor(t *testing.T, dep deps.ID, fromImage string) *itEnv {
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

	// Not t.TempDir(): t.TempDir()'s own cleanup FAILS the test when the
	// directory will not go, and here it sometimes legitimately will not.
	//
	// What resists removal is NOT the dump. That lands in a 1777 scratch
	// directory THIS process created, and the sticky bit restricts unlink to
	// the file's owner OR the DIRECTORY's owner — we are the latter — so a
	// dump written by the container's uid 999 unlinks with no privilege at
	// all (measured: dir 1777 owned by the test uid, file owned by the
	// mapped subuid, os.RemoveAll returns nil), and the plan removes that
	// directory itself on both the success and the rollback path anyway.
	//
	// What resists is the CLUSTER: the postgres container writes PGDATA under
	// volumes/ as uid 999 with 0700 DIRECTORIES. Unlinking needs write
	// permission on the parent directory, and here the parent is neither
	// readable nor writable nor ours to chmod (chmod requires ownership), so
	// os.RemoveAll stops at `openfdat .../postgres2: permission denied` and
	// no mode fixing this process is allowed to perform can open it.
	//
	// Under both invocations this target supports the process IS privileged
	// over uid 999 — `unshare --user --map-auto --map-root-user` maps the
	// subuid range, `sudo` is real root (see the header of this file) — so a
	// run that got as far as writing a cluster also removes it. The leftover
	// is the MISCONFIGURED run: requireReadableCluster fatals on exactly that
	// uid gap, and by then the seed has written a cluster this process cannot
	// delete. That is a leftover to report, not a migration that failed: the
	// removal is best-effort and names what is left behind, under $TMPDIR
	// with this namespace's own id in the name.
	base, err := os.MkdirTemp("", nsID+"-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		rmErr := os.RemoveAll(base)
		if rmErr == nil {
			return
		}
		hint := ""
		if errors.Is(rmErr, fs.ErrPermission) {
			hint = "\n\tit belongs to the container's uid: remove it with the privilege this test needs anyway —\n" +
				"\tunshare --user --map-auto --map-root-user rm -rf " + base + "   (rootless Docker)\n" +
				"\tsudo rm -rf " + base + "   (rootful daemon)"
		}
		t.Logf("leftover test directory %s: %v%s", base, rmErr, hint)
	})

	nsCfg := &namespace.Config{
		ID:             nsID,
		Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
		Proxy:          namespace.ProxyProps{Port: 80},
	}
	rt := namespace.NewRuntime(nsCfg, dc, base)
	t.Cleanup(rt.Shutdown)
	// The pin is what the namespace's data runs on; the migration moves it.
	rt.SetDependencyState(dep, deps.DependencyState{Image: fromImage})

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
	d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(d.bgCancel)

	// The postgres def binds ./postgres/{postgresql,pg_hba}.conf and the init
	// script out of the namespace's runtime directory, and the rabbitmq def
	// binds ./rabbitmq/citeck-memory.conf; a real namespace has them on disk
	// from its last reload, so materialize them the same way
	// generateAndWriteRuntimeFiles does.
	genResp, err := namespace.Generate(nsCfg, act.bundleDef, act.workspaceConfig, act.systemSecrets,
		namespace.GenerateOpts{DependencyStates: rt.DependencyStates()})
	require.NoError(t, err)
	writeRuntimeFiles(base, genResp.Files, nil)
	// What the last generation did with the pins. The dependency LIST route
	// reads it (a dependency the last generation did not emit is left out
	// entirely), so a test that asks the daemon what it would offer has to be
	// given the same input the loader gives it.
	act.dependencies = genResp.Dependencies
	act.dependencyUpgrades = genResp.DependencyUpgrades

	e := &itEnv{
		env: &observedEnv{Env: d.newDepsEnv(act)},
		rt:  rt, dc: dc, d: d, base: base, reloads: reloads,
	}
	// The routes must see the SAME Env the test drives the plan with —
	// otherwise the observer records nothing a route did, and the two would
	// measure two different worlds.
	d.depsEnvFn = func(activeNamespace) migrate.Env { return e.env }
	e.mux = http.NewServeMux()
	d.registerRoutes(e.mux)
	return e
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

	def, err := e.env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: itFromImage})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: itSeedContainer})
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
	_, err := e.env.ReadVolumeFile(ctx, itVolume(1), "PG_VERSION")
	if errors.Is(err, fs.ErrPermission) {
		itFatalUIDGap(t, "read "+itVolume(1)+"/PG_VERSION", err)
	}
	require.NoError(t, err)
}

// requirePrivilegeOverContainerFiles is requireReadableCluster's shape for a
// dependency whose data carries no version marker to read.
//
// It asks the capability the test actually needs rather than a proxy for it:
// the utils container (root) makes a file owned by a container uid, and this
// process must be able to remove it. That is what a rollback's RemoveVolume
// does to a copy the image wrote, and what the harness's own cleanup does to
// the namespace directory — and under rootless Docker without the mapping,
// both fail with EACCES several minutes in, dressed as something else.
//
// It deliberately does NOT probe the data volume: the copy-upgrade tests
// assert that the source volume is byte-identical afterwards, and a probe file
// written into it — even one removed again — would be the test mutating the
// very thing it is about to measure.
func (e *itEnv) requirePrivilegeOverContainerFiles(ctx context.Context, t *testing.T) {
	t.Helper()
	dir := filepath.Join(e.base, "uid-probe")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, e.dc.EnsureUtilsImage(ctx))
	out, code, err := e.dc.RunUtilsContainer(ctx,
		[]string{"sh", "-c", "mkdir -p /probe/d && : > /probe/d/f && chown -R 999:999 /probe"},
		[]string{dir + ":/probe"})
	require.NoErrorf(t, err, "uid probe: %s", out)
	require.Zerof(t, code, "uid probe: %s", out)
	if err := os.RemoveAll(dir); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			itFatalUIDGap(t, "remove a file a container created", err)
		}
		require.NoError(t, err)
	}
}

// itFatalUIDGap fails with the fix rather than with the symptom. Both callers
// hit the SAME condition — this process is not privileged over the uid the
// image runs as — and a second wording of it would be a second thing to keep
// true.
func itFatalUIDGap(t *testing.T, what string, err error) {
	t.Helper()
	t.Fatalf("cannot %s as this user: %v\n"+
		"Server mode expects the launcher to own what the container wrote (the root daemon a server install runs).\n"+
		"Under ROOTLESS Docker, run the test in a user namespace that maps the subuid range:\n"+
		"  unshare --user --map-auto --map-root-user make test-integration-deps\n"+
		"With a ROOTFUL daemon a docker-group user hits the same EACCES (the image's uid is a real uid there)\n"+
		"and a user namespace does not help — run it as root instead:\n"+
		"  sudo make test-integration-deps",
		what, err)
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

	// The plan's own exported list, never a copy: a step renamed in postgres.go
	// must fail HERE rather than quietly stop being asserted.
	assert.Equal(t, migrate.PostgresStepIDs(), steps)

	// The evidence the fakes cannot produce: what psql actually printed while
	// replaying a real pg_dumpall script into PostgreSQL 18.
	//
	// The prefix comes from the plan's own exported builder, never a copy: a
	// flag added or reordered there would silently stop matching, and a miss
	// must FAIL rather than skip — "no stderr recorded" is exactly what a
	// broken lookup looks like, and it would delete this assertion with a
	// green run.
	restoreLog, ok := e.env.stderrFor(strings.Join(migrate.RestoreCommandPrefix(), " "))
	require.Truef(t, ok, "no command matching the restore prefix %q produced stderr; recorded: %v",
		strings.Join(migrate.RestoreCommandPrefix(), " "), e.env.recordedCommands())
	t.Logf("restore stderr (exit %d):\n%s", restoreLog.code, strings.TrimSpace(restoreLog.stderr))
	assert.Contains(t, restoreLog.stderr, `role "postgres" already exists`,
		"the one error the restore tolerates is the one a real dump always produces")

	// --- the pin, the journal and the verdict --------------------------------
	assert.Equal(t, itToImage, e.rt.DependencyStates()[deps.Postgres].Image)
	assert.Nil(t, e.rt.MigrationJournal(), "a committed migration clears the journal")
	last := e.rt.LastDependencyMigration()
	require.NotNil(t, last)
	assert.True(t, last.OK(), "verdict: %s", last.Error)
	assert.Equal(t, itVolume(1), last.OldVolume)
	assert.Equal(t, 1, e.reloads.get(), "finalize reloads the namespace exactly once")

	// --- the volumes ---------------------------------------------------------
	assert.FileExists(t, filepath.Join(e.volumeDir(itVolume(1)), "PG_VERSION"),
		"the old cluster is only ever READ")
	pgv, err := os.ReadFile(filepath.Join(e.volumeDir(itVolume(2)), "18", "docker", "PG_VERSION"))
	require.NoError(t, err)
	assert.Equal(t, "18", strings.TrimSpace(string(pgv)))
	assert.NoDirExists(t, depsMigrationDir(e.base), "finalize removes the scratch dir AND its empty parent")

	// --- what the migrated cluster actually holds ----------------------------
	def18, err := e.env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: itToImage, VolumeGen: 2})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def18, deps.TempContainerOpts{Name: itCheckContainer})
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

	before, err := os.ReadFile(filepath.Join(e.volumeDir(itVolume(1)), "PG_VERSION"))
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
			dumpSize = itDumpBytes(e.env.DumpDir(deps.Postgres))
			targetVolume, _ = e.env.VolumeExists(ctx, itVolume(2))
			journaledVolume = j.CreatedVolume

			def, defErr := e.env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: itToImage, VolumeGen: 2})
			if defErr != nil {
				return fmt.Errorf("generate the sabotaged target def: %w", defErr)
			}
			// Pulls, starts, and is not a PostgreSQL server — so the target
			// container EXISTS and runs when the step fails, which is what the
			// rollback has to clean up. alpine:3 is a PROP, and this closure
			// deliberately short-circuits the real start-target step's
			// readiness wait (migrate.waitReady would poll an image that never
			// answers for its full 5-minute budget) — so the wording below is
			// the TEST's, and must never be mistaken for the engine's.
			def.Image = itBadImage
			// PID 1 with no signal handler ignores SIGTERM, so the rollback's
			// stop pays the full graceful-stop grace before the kill — the
			// real path, and the reason this test takes ~30s longer than the
			// happy one.
			def.Cmd = []string{"sleep", "600"}
			if _, runErr := e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: migrate.DstContainer}); runErr != nil {
				return fmt.Errorf("start the sabotaged target: %w", runErr)
			}
			dstRunning, _ = e.env.ContainerRunning(ctx, migrate.DstContainer)
			return fmt.Errorf("test sabotage: %s runs %s, which is not a PostgreSQL server",
				migrate.DstContainer, itBadImage)
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
	assert.Equal(t, itVolume(2), journaledVolume, "the volume is journaled before it is created")
	assert.True(t, dstRunning, "the sabotaged target container must be running when the step fails")

	// --- nothing moved -------------------------------------------------------
	assert.Equal(t, itFromImage, e.rt.DependencyStates()[deps.Postgres].Image, "the pin never moves on failure")
	after, err := os.ReadFile(filepath.Join(e.volumeDir(itVolume(1)), "PG_VERSION"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the source cluster is still a PostgreSQL 17 data directory")

	// --- nothing was left behind ---------------------------------------------
	assert.NoDirExists(t, e.volumeDir(itVolume(2)))
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
	def17, err := e.env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: itFromImage})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def17, deps.TempContainerOpts{Name: itCheckContainer})
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

// itVolume is the plain name of one generation of the postgres data volume,
// asked of the registry rather than spelled out: the names are a function of
// the generation counter, and a literal here would keep passing while the
// launcher mounted something else entirely.
func itVolume(gen int) string { return itVolumeOf(deps.Postgres, gen) }

// itVolumeOf is the same for any dependency.
func itVolumeOf(id deps.ID, gen int) string {
	d, ok := deps.Lookup(id)
	if !ok {
		panic(string(id) + " is not registered")
	}
	return deps.VolumeName(d, gen)
}

// itManifestMount is where a manifest run mounts the volume it measures.
const itManifestMount = "/vol"

// itManifestScript prints one line per entry under the mounted volume:
//
//	<path relative to the volume root>|<type>|<size>|<uid>|<gid>|<mode>
//
// It runs in the utils container as ROOT, which is the only way to walk a tree
// an image wrote as its own uid — and the only way to read the same numbers
// twice, since what the test process can see of that tree depends on how it
// was invoked.
const itManifestScript = "cd " + itManifestMount + ` && find . -exec stat -c '%n|%F|%s|%u|%g|%a' {} +`

// volumeManifest is a data volume's exact shape: every entry, its type, size,
// owner and mode. Ruling on OPEN QUESTION 4 — content hashes add no signal
// (nothing in a copy-upgrade plan opens the source for writing) and cost
// minutes on a real volume, while a size/mode manifest already catches every
// mutation shape the launcher can cause.
//
// It must be taken with NOTHING RUNNING on the volume, before and after: a
// booting ZooKeeper rewrites its newest snapshot (zk-experiment.md) and a
// RabbitMQ node writes a pid file, so a manifest taken beside a live container
// measures the container, not the migration.
func (e *itEnv) volumeManifest(ctx context.Context, t *testing.T, volume string) []string {
	t.Helper()
	require.NoError(t, e.dc.EnsureUtilsImage(ctx))
	out, code, err := e.dc.RunUtilsContainer(ctx,
		[]string{"sh", "-c", itManifestScript}, []string{e.volumeDir(volume) + ":" + itManifestMount + ":ro"})
	require.NoErrorf(t, err, "manifest of %s: %s", volume, out)
	require.Zerof(t, code, "manifest of %s: %s", volume, out)
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, "\r")
		// The mount's own root is "." on both sides of every comparison and
		// says nothing; everything else that is not a manifest line is a
		// diagnostic the utils container printed, and dropping it silently is
		// how a manifest of nothing compares equal to another manifest of
		// nothing — so the count is asserted by the callers.
		if !strings.HasPrefix(line, "./") {
			continue
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}

// itVolumeUsageScript prints what one mounted volume costs, twice over: the
// blocks it OCCUPIES and the bytes its regular files CONTAIN.
//
// The two are the same number for ordinary data and wildly different for the
// files these dependencies write — ZooKeeper preallocates its transaction log
// to 64 MiB and Mnesia writes its schema with holes — so what a copy costs on
// the destination has to be measured rather than assumed from the source.
const itVolumeUsageScript = "cd " + itManifestMount +
	` && echo "kb $(du -sk . | cut -f1)" && echo "bytes $(find . -type f -exec stat -c %s {} + | awk '{s+=$1} END {print s+0}')"`

// volumeUsage answers (allocated KiB, apparent bytes) for a data volume.
func (e *itEnv) volumeUsage(ctx context.Context, t *testing.T, volume string) (allocatedKB, apparentBytes int64) {
	t.Helper()
	require.NoError(t, e.dc.EnsureUtilsImage(ctx))
	out, code, err := e.dc.RunUtilsContainer(ctx,
		[]string{"sh", "-c", itVolumeUsageScript}, []string{e.volumeDir(volume) + ":" + itManifestMount + ":ro"})
	require.NoErrorf(t, err, "usage of %s: %s", volume, out)
	require.Zerof(t, code, "usage of %s: %s", volume, out)
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, convErr := strconv.ParseInt(f[1], 10, 64)
		if convErr != nil {
			continue
		}
		switch f[0] {
		case "kb":
			allocatedKB = n
		case "bytes":
			apparentBytes = n
		}
	}
	require.NotZerof(t, apparentBytes, "usage of %s measured nothing: %s", volume, out)
	return allocatedKB, apparentBytes
}

// itOwnershipOf projects a manifest onto what a COPY has to reproduce: path,
// type, owner and mode, plus the size of everything that is not a directory.
//
// A directory's st_size is an allocation detail of the filesystem — the number
// of entries it has held, not the number it holds — so comparing it between a
// grown source directory and a freshly written copy of it would report a
// difference that is not one. Every entry BELOW it is still compared, so a
// directory whose contents differ cannot hide here.
func itOwnershipOf(manifest []string) []string {
	out := make([]string, 0, len(manifest))
	for _, line := range manifest {
		f := strings.Split(line, "|")
		if len(f) != 6 {
			out = append(out, line) // unparsable: compare it verbatim
			continue
		}
		if f[1] == "directory" {
			f[2] = "-"
		}
		out = append(out, strings.Join(f, "|"))
	}
	return out
}

// itManifestPaths is a manifest reduced to the entries it holds.
//
// It is the right comparison for a copy a container has already RUN on: a
// booting server legitimately adds files and rewrites its own (ZooKeeper
// snapshots right after loading and preallocates a fresh transaction log), so
// the question "is the real data in here?" is about the entries, not about
// their bytes.
func itManifestPaths(manifest []string) []string {
	out := make([]string, 0, len(manifest))
	for _, line := range manifest {
		path, _, _ := strings.Cut(line, "|")
		out = append(out, path)
	}
	return out
}

// itManifestEntry finds one entry of a manifest by its path.
func itManifestEntry(manifest []string, relPath string) (string, bool) {
	for _, line := range manifest {
		if strings.HasPrefix(line, relPath+"|") {
			return line, true
		}
	}
	return "", false
}
