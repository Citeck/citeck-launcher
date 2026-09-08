package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// createdContainer records one CreateContainerWith call: everything the daemon
// asked Docker for, so a test can assert on the container that WOULD exist
// without an engine.
type createdContainer struct {
	def         appdef.ApplicationDef
	volumesBase string
	opts        docker.ContainerCreateOpts
}

// fakeDepsDocker is the depsDocker seam's in-memory stand-in. Every call is
// appended to calls in order, which is what lets the RunAppDef test assert
// that NOTHING but create+start happened — the Env's hard precondition that a
// temp container runs the container and nothing around it.
type fakeDepsDocker struct {
	mu    sync.Mutex
	calls []string

	created    []createdContainer
	newID      string
	networkErr error
	createErr  error
	startErr   error

	inspect    map[string]container.InspectResponse
	inspectErr map[string]error

	execStdout string
	execStderr string
	execCode   int
	execErr    error

	utilsOut  string
	utilsCode int
	utilsErr  error
	utilsBind []string

	ensureUtilsErr  error
	ensureUtilsRuns int

	volumes    map[string]*volume.Volume
	volumeErr  error
	volSize    map[string]int64
	removedVol []string

	pulled   []string
	pullPct  []int
	pullErr  error
	pullAuth *docker.RegistryAuth
}

func newFakeDepsDocker() *fakeDepsDocker {
	return &fakeDepsDocker{
		newID:      "cid-1",
		inspect:    map[string]container.InspectResponse{},
		inspectErr: map[string]error{},
		volumes:    map[string]*volume.Volume{},
		volSize:    map[string]int64{},
	}
}

func (f *fakeDepsDocker) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeDepsDocker) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeDepsDocker) ContainerName(app string) string { return "citeck_" + app + "_ns1" }

func (f *fakeDepsDocker) CreateNetwork(context.Context) (string, error) {
	f.record("network")
	return "citeck_ns1", f.networkErr
}

func (f *fakeDepsDocker) CreateContainerWith(_ context.Context, app appdef.ApplicationDef,
	volumesBase string, opts docker.ContainerCreateOpts,
) (string, error) {
	f.record("create:" + opts.Name + ":" + app.Image)
	if f.createErr != nil {
		return "", f.createErr
	}
	f.mu.Lock()
	f.created = append(f.created, createdContainer{def: app, volumesBase: volumesBase, opts: opts})
	f.mu.Unlock()
	return f.newID, nil
}

func (f *fakeDepsDocker) StartContainer(_ context.Context, id string) error {
	f.record("start:" + id)
	return f.startErr
}

func (f *fakeDepsDocker) RemoveContainer(_ context.Context, id string) error {
	f.record("rmcontainer:" + id)
	return nil
}

func (f *fakeDepsDocker) StopAndRemoveContainer(_ context.Context, name string, timeout int) error {
	f.record(fmt.Sprintf("stoprm:%s:%d", name, timeout))
	return f.inspectErr["stoprm:"+name]
}

func (f *fakeDepsDocker) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	f.record("inspect:" + id)
	if err := f.inspectErr[id]; err != nil {
		return container.InspectResponse{}, err
	}
	return f.inspect[id], nil
}

func (f *fakeDepsDocker) ExecInContainerSplit(_ context.Context, id string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	f.record("exec:" + id + ":" + strings.Join(cmd, " "))
	return f.execStdout, f.execStderr, f.execCode, f.execErr
}

func (f *fakeDepsDocker) PullImageWithProgress(_ context.Context, img string,
	auth *docker.RegistryAuth, fn docker.PullProgressFn,
) error {
	f.record("pull:" + img)
	f.mu.Lock()
	f.pulled = append(f.pulled, img)
	f.pullAuth = auth
	f.mu.Unlock()
	if fn != nil {
		for _, pct := range f.pullPct {
			fn(0, 0, pct)
		}
	}
	return f.pullErr
}

func (f *fakeDepsDocker) EnsureUtilsImage(context.Context) error {
	f.record("ensureutils")
	f.mu.Lock()
	f.ensureUtilsRuns++
	f.mu.Unlock()
	return f.ensureUtilsErr
}

func (f *fakeDepsDocker) RunUtilsContainer(_ context.Context, cmd, binds []string) (output string, exitCode int, err error) {
	f.record("utils:" + strings.Join(cmd, " "))
	f.mu.Lock()
	f.utilsBind = binds
	f.mu.Unlock()
	return f.utilsOut, f.utilsCode, f.utilsErr
}

func (f *fakeDepsDocker) GetVolumeByOriginalName(_ context.Context, name string) (*volume.Volume, error) {
	f.record("getvol:" + name)
	if f.volumeErr != nil {
		return nil, f.volumeErr
	}
	return f.volumes[name], nil
}

func (f *fakeDepsDocker) CreateVolume(_ context.Context, name string) (string, error) {
	f.record("createvol:" + name)
	if f.volumeErr != nil {
		return "", f.volumeErr
	}
	scoped := "citeck_volume_" + name
	f.mu.Lock()
	f.volumes[name] = &volume.Volume{Name: scoped}
	f.mu.Unlock()
	return scoped, nil
}

func (f *fakeDepsDocker) RemoveVolume(_ context.Context, name string) error {
	f.record("rmvol:" + name)
	f.mu.Lock()
	f.removedVol = append(f.removedVol, name)
	f.mu.Unlock()
	return f.volumeErr
}

func (f *fakeDepsDocker) VolumeSize(_ context.Context, name string) (int64, error) {
	f.record("volsize:" + name)
	return f.volSize[name], nil
}

var _ depsDocker = (*fakeDepsDocker)(nil)

// newTestDepsEnv builds a depsEnv over the fake Docker seam and a temp
// volumesBase, in SERVER mode unless the test says otherwise.
func newTestDepsEnv(t *testing.T, fake *fakeDepsDocker) (env *depsEnv, volumesBase string) {
	t.Helper()
	base := t.TempDir()
	d := &Daemon{}
	env = d.newDepsEnv(activeNamespace{
		nsConfig:    &namespace.Config{ID: "ns1"},
		volumesBase: base,
	})
	env.dc = fake
	env.probe.dc = fake
	return env, base
}

// --- containers ------------------------------------------------------------

// RunAppDef must create the container under the TEMP name with the temp label,
// with published ports and network aliases dropped and the scratch bind
// appended — and it must run NOTHING around it: no init actions, no probes.
// The call log is the proof: create + start and nothing else.
func TestRunAppDefRunsTheContainerAndNothingAroundIt(t *testing.T) {
	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)

	def := appdef.ApplicationDef{
		Name:           "postgres",
		Image:          "postgres:17.5",
		Ports:          []string{"14523:5432"},
		NetworkAliases: []string{"pg", "db"},
		Volumes:        []string{"postgres2:/var/lib/postgresql/data"},
		InitActions: []appdef.AppInitAction{
			{Exec: []string{"/init_db_and_user.sh", "citeck_emodel"}},
		},
		StartupConditions: []appdef.StartupCondition{
			{Log: &appdef.LogStartupCondition{Pattern: "ready"}},
		},
	}

	id, err := env.RunAppDef(context.Background(), def, migrate.SrcContainer, []string{"/host/dump:/citeck/depsmig"})
	require.NoError(t, err)
	assert.Equal(t, "cid-1", id)

	assert.Equal(t, []string{
		"network",
		"stoprm:citeck_depsmig-src_ns1:0",
		"create:depsmig-src:postgres:17.5",
		"start:cid-1",
	}, fake.Calls(), "a temp container is created and started — nothing runs its init actions or probes")

	require.Len(t, fake.created, 1)
	got := fake.created[0]
	assert.Equal(t, migrate.SrcContainer, got.opts.Name)
	assert.Equal(t, docker.LabelTempValue, got.opts.ExtraLabels[docker.LabelTemp])
	assert.Equal(t, base, got.volumesBase)
	assert.Nil(t, got.def.Ports, "published ports are stripped")
	assert.Nil(t, got.def.NetworkAliases, "the temp container answers only to its own name")
	assert.Equal(t, []string{"postgres2:/var/lib/postgresql/data", "/host/dump:/citeck/depsmig"}, got.def.Volumes)
	assert.NotEmpty(t, got.def.InitActions,
		"the def keeps its init actions — the Env simply never executes them")

	// The caller's def is untouched: RunAppDef works on its own copy.
	assert.Equal(t, []string{"14523:5432"}, def.Ports)
	assert.Equal(t, []string{"postgres2:/var/lib/postgresql/data"}, def.Volumes)
}

func TestRunAppDefRemovesTheContainerWhenItCannotBeStarted(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.startErr = errors.New("boom")
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.RunAppDef(context.Background(), appdef.ApplicationDef{Name: "postgres"}, "depsmig-dst", nil)
	require.Error(t, err)
	assert.Contains(t, fake.Calls(), "rmcontainer:cid-1",
		"a container that would not start must not be left behind")
}

func TestRunAppDefFailsWhenTheNetworkCannotBeEnsured(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.networkErr = errors.New("no network")
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.RunAppDef(context.Background(), appdef.ApplicationDef{Name: "postgres"}, "depsmig-src", nil)
	require.Error(t, err)
	assert.NotContains(t, fake.Calls(), "create:depsmig-src:")
}

func TestContainerRunningIsScopedAndTreatsNotFoundAsAnAnswer(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	fake.inspect["citeck_depsmig-src_ns1"] = container.InspectResponse{
		State: &container.State{Running: true},
	}
	fake.inspectErr["citeck_depsmig-dst_ns1"] = notFoundErr{}
	fake.inspectErr["citeck_other_ns1"] = errors.New("engine is down")

	ok, err := env.ContainerRunning(context.Background(), migrate.SrcContainer)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = env.ContainerRunning(context.Background(), migrate.DstContainer)
	require.NoError(t, err, "an absent container is an answer, not a failure")
	assert.False(t, ok)

	_, err = env.ContainerRunning(context.Background(), "other")
	require.Error(t, err, "a failed inspect says nothing about the container")
}

func TestExecReturnsBothStreamsAndTheExitCodeSeparately(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.execStdout = "1 row"
	fake.execStderr = "NOTICE: something"
	fake.execCode = 3
	env, _ := newTestDepsEnv(t, fake)

	stdout, stderr, code, err := env.Exec(context.Background(), migrate.SrcContainer, []string{"psql", "-c", "select 1"})
	require.NoError(t, err, "a command that RAN and failed is not an error")
	assert.Equal(t, "1 row", stdout)
	assert.Equal(t, "NOTICE: something", stderr)
	assert.Equal(t, 3, code)
	assert.Contains(t, fake.Calls(), "exec:citeck_depsmig-src_ns1:psql -c select 1")
}

func TestStopRemoveTreatsNotFoundAsSuccess(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	fake.inspectErr["stoprm:citeck_depsmig-src_ns1"] = notFoundErr{}

	require.NoError(t, env.StopRemove(context.Background(), migrate.SrcContainer))

	fake.inspectErr["stoprm:citeck_depsmig-dst_ns1"] = errors.New("engine is down")
	require.Error(t, env.StopRemove(context.Background(), migrate.DstContainer))
}

// --- volumes ---------------------------------------------------------------

func TestServerVolumeLifecycleIsTheBindDirectory(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	ctx := context.Background()

	exists, err := env.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, env.CreateVolume(ctx, "postgres3"))
	exists, err = env.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.DirExists(t, filepath.Join(base, "volumes", "postgres3"))

	require.NoError(t, os.WriteFile(filepath.Join(base, "volumes", "postgres3", "PG_VERSION"), []byte("18\n"), 0o600))
	size, err := env.VolumeSize(ctx, "postgres3")
	require.NoError(t, err)
	assert.Equal(t, int64(3), size)

	require.NoError(t, env.RemoveVolume(ctx, "postgres3"))
	exists, err = env.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.False(t, exists)
	require.NoError(t, env.RemoveVolume(ctx, "postgres3"), "removing an absent volume is success")

	size, err = env.VolumeSize(ctx, "gone")
	require.NoError(t, err, "an absent volume measures 0, it is not a failure")
	assert.Zero(t, size)

	assert.Empty(t, fake.Calls(), "server mode never asks the engine where a volume lives")
}

func TestDesktopVolumeLifecycleGoesThroughTheScopedNamedVolume(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	ctx := context.Background()

	exists, err := env.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, env.CreateVolume(ctx, "postgres3"))
	exists, err = env.VolumeExists(ctx, "postgres3")
	require.NoError(t, err)
	assert.True(t, exists)

	fake.volSize["citeck_volume_postgres3"] = 4096
	size, err := env.VolumeSize(ctx, "postgres3")
	require.NoError(t, err)
	assert.Equal(t, int64(4096), size)

	require.NoError(t, env.RemoveVolume(ctx, "postgres3"))
	assert.Equal(t, []string{"citeck_volume_postgres3"}, fake.removedVol,
		"the SCOPED name is what Docker knows the volume by")
	require.NoError(t, env.RemoveVolume(ctx, "postgres3"), "removing an absent volume is success")

	size, err = env.VolumeSize(ctx, "gone")
	require.NoError(t, err)
	assert.Zero(t, size)
}

func TestDesktopVolumeErrorsAreReportedNotSwallowed(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	fake.volumeErr = errors.New("engine is down")
	env, _ := newTestDepsEnv(t, fake)
	ctx := context.Background()

	_, err := env.VolumeExists(ctx, "postgres3")
	require.Error(t, err)
	require.Error(t, env.CreateVolume(ctx, "postgres3"))
	require.Error(t, env.RemoveVolume(ctx, "postgres3"))
	_, err = env.VolumeSize(ctx, "postgres3")
	require.Error(t, err)
}

func TestVolumeFreeBytesOnTheServerMeasuresTheVolumesFilesystem(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes"), 0o755))

	free, err := env.VolumeFreeBytes(context.Background(), "postgres2")
	require.NoError(t, err)
	assert.Positive(t, free)
	assert.Empty(t, fake.Calls(), "the host filesystem answers directly — no container")

	host, err := env.HostFreeBytes()
	require.NoError(t, err)
	assert.Positive(t, host)
}

func TestFreeBytesReportsAMissingDirectoryAsAnErrorNotAsZero(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	env.act.volumesBase = filepath.Join(t.TempDir(), "not-there")

	_, err := env.HostFreeBytes()
	require.Error(t, err, `"0 bytes free" would be read as a full disk`)
}

func TestVolumeFreeBytesOnTheDesktopAsksTheEngineThroughAUtilsContainer(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	fake.volumes["postgres2"] = &volume.Volume{Name: "citeck_volume_postgres2"}
	fake.utilsOut = "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
		"/dev/vda1         61255492 20154752  38492340      35% /vol\n"
	env, _ := newTestDepsEnv(t, fake)

	free, err := env.VolumeFreeBytes(context.Background(), "postgres2")
	require.NoError(t, err)
	assert.Equal(t, int64(38492340)*1024, free)
	assert.Equal(t, []string{"citeck_volume_postgres2:/vol:ro"}, fake.utilsBind)
	assert.Equal(t, 1, fake.ensureUtilsRuns,
		"the utils image must be on the host before a utils container is run")

	fake.utilsCode = 1
	fake.utilsOut = "df: /vol: No such file or directory"
	_, err = env.VolumeFreeBytes(context.Background(), "postgres2")
	require.Error(t, err)
}

func TestVolumeFreeBytesOnTheDesktopFailsWhenTheProbeVolumeIsGone(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.VolumeFreeBytes(context.Background(), "postgres2")
	require.Error(t, err)
}

func TestParseDfAvailableKB(t *testing.T) {
	out := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
		"/dev/vda1         61255492 20154752  38492340      35% /vol\n"
	kb, err := parseDfAvailableKB(out)
	require.NoError(t, err)
	assert.Equal(t, int64(38492340), kb)

	// A long device name makes df wrap the row; the numbers are on the next
	// line and the header must never be mistaken for them.
	wrapped := "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
		"/dev/mapper/a-very-long-logical-volume-name\n" +
		"                  61255492 20154752    123456      35% /vol\n"
	kb, err = parseDfAvailableKB(wrapped)
	require.NoError(t, err)
	assert.Equal(t, int64(123456), kb)

	_, err = parseDfAvailableKB("garbage")
	require.Error(t, err)
	_, err = parseDfAvailableKB("")
	require.Error(t, err)
}

// ReadVolumeFile is the seeding probe's rule, and the Env must not grow a
// second copy of it: server mode reads the bind directory, desktop mode goes
// through the engine.
func TestReadVolumeFileIsTheSeedingProbesRule(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	dir := filepath.Join(base, "volumes", "postgres2")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("17\n"), 0o600))

	raw, err := env.ReadVolumeFile(context.Background(), "postgres2", "PG_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "17\n", raw)

	_, err = env.ReadVolumeFile(context.Background(), "postgres3", "18/docker/PG_VERSION")
	require.ErrorIs(t, err, errVolumeFileNotFound)
}

// --- host files ------------------------------------------------------------

func TestDumpDirIsUnderTheVolumesBase(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "base")
	env := (&Daemon{}).newDepsEnv(activeNamespace{volumesBase: base})
	assert.Equal(t, filepath.Join(base, "deps-migration", "postgres"), env.DumpDir(deps.Postgres))
}

// EnsureDir must leave the directory writable by the CONTAINER's own uid — the
// daemon is root, postgres is 999 — so the mode is re-applied explicitly:
// MkdirAll honors the umask and leaves an existing directory's mode alone.
func TestEnsureDirIsWorldWritableAndSticky(t *testing.T) {
	env, _ := newTestDepsEnv(t, newFakeDepsDocker())
	base := t.TempDir()
	nested := filepath.Join(base, "deps-migration", "postgres")

	require.NoError(t, env.EnsureDir(nested))
	st, err := os.Stat(nested)
	require.NoError(t, err)
	assert.Equal(t, os.ModeSticky|os.FileMode(0o777), st.Mode().Perm()|(st.Mode()&os.ModeSticky))

	// An existing directory with a restrictive mode is re-opened, not left as
	// it was: MkdirAll on an existing path is a no-op.
	tight := filepath.Join(base, "tight")
	require.NoError(t, os.MkdirAll(tight, 0o700))
	require.NoError(t, env.EnsureDir(tight))
	st, err = os.Stat(tight)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o777), st.Mode().Perm())
	assert.NotZero(t, st.Mode()&os.ModeSticky)
}

func TestRemoveDirIfEmptyLeavesASharedParentAlone(t *testing.T) {
	env, _ := newTestDepsEnv(t, newFakeDepsDocker())
	base := t.TempDir()

	shared := filepath.Join(base, "deps-migration")
	other := filepath.Join(shared, "rabbitmq")
	require.NoError(t, os.MkdirAll(other, 0o755))

	require.NoError(t, env.RemoveDirIfEmpty(shared), "a non-empty directory is left alone, and that is not an error")
	assert.DirExists(t, shared)
	assert.DirExists(t, other)

	require.NoError(t, env.RemoveDirIfEmpty(other))
	assert.NoDirExists(t, other)
	require.NoError(t, env.RemoveDirIfEmpty(other), "an absent directory is already gone")

	require.NoError(t, env.RemoveDirIfEmpty(shared))
	assert.NoDirExists(t, shared)
}

func TestRemoveDirDeletesTheTreeAndToleratesAnAbsentOne(t *testing.T) {
	env, _ := newTestDepsEnv(t, newFakeDepsDocker())
	base := t.TempDir()
	dir := filepath.Join(base, "deps-migration", "postgres")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dump.sql"), []byte("x"), 0o600))

	require.NoError(t, env.RemoveDir(dir))
	assert.NoDirExists(t, dir)
	require.NoError(t, env.RemoveDir(dir))
}

func TestFileSizeReportsAnAbsentFileAsZero(t *testing.T) {
	env, _ := newTestDepsEnv(t, newFakeDepsDocker())
	base := t.TempDir()
	dump := filepath.Join(base, "dump.sql")

	size, err := env.FileSize(dump)
	require.NoError(t, err, "the dump does not exist until pg_dumpall creates it")
	assert.Zero(t, size)

	require.NoError(t, os.WriteFile(dump, []byte("hello"), 0o600))
	size, err = env.FileSize(dump)
	require.NoError(t, err)
	assert.Equal(t, int64(5), size)
}

// --- images ----------------------------------------------------------------

func TestPullImageReportsPercentProgress(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.pullPct = []int{10, 55, 100}
	env, _ := newTestDepsEnv(t, fake)

	var seen []float64
	require.NoError(t, env.PullImage(context.Background(), "postgres:18", func(pct float64) {
		seen = append(seen, pct)
	}))
	assert.Equal(t, []string{"postgres:18"}, fake.pulled)
	assert.Equal(t, []float64{10, 55, 100}, seen)
	assert.Nil(t, fake.pullAuth, "no workspace config ⇒ no registry auth, not a panic")
}

func TestPullImageSurfacesTheFailure(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.pullErr = errors.New("unauthorized")
	env, _ := newTestDepsEnv(t, fake)

	err := env.PullImage(context.Background(), "postgres:18", func(float64) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

// --- namespace -------------------------------------------------------------

func TestNamespaceIDComesFromTheSnapshot(t *testing.T) {
	env, _ := newTestDepsEnv(t, newFakeDepsDocker())
	assert.Equal(t, "ns1", env.NamespaceID())
	assert.Empty(t, (&Daemon{}).newDepsEnv(activeNamespace{}).NamespaceID())
}

func TestIsRunningIsFalseOnlyWhenTheNamespaceIsStopped(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt})

	rt.SetStatusForTest(namespace.NsStatusStopped)
	assert.False(t, env.IsRunning())
	rt.SetStatusForTest(namespace.NsStatusRunning)
	assert.True(t, env.IsRunning())
	rt.SetStatusForTest(namespace.NsStatusStarting)
	assert.True(t, env.IsRunning())

	assert.False(t, (&Daemon{}).newDepsEnv(activeNamespace{}).IsRunning())
}

func TestStopNamespaceWaitsForStopped(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt})
	env.stopPoll = time.Millisecond
	env.stopWait = 5 * time.Second

	rt.SetStatusForTest(namespace.NsStatusRunning)
	go func() {
		time.Sleep(20 * time.Millisecond)
		rt.SetStatusForTest(namespace.NsStatusStopped)
	}()
	require.NoError(t, env.StopNamespace(context.Background()))
}

// A namespace that will not stop must FAIL the step: the plan has not created
// anything yet, so failing here costs nothing, while carrying on would dump a
// live cluster.
func TestStopNamespaceGivesUpRatherThanWaitingForever(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt})
	env.stopPoll = time.Millisecond
	env.stopWait = 30 * time.Millisecond
	rt.SetStatusForTest(namespace.NsStatusRunning)

	err := env.StopNamespace(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNNING")
}

func TestStopNamespaceIsANoOpOnAStoppedNamespace(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt})
	require.NoError(t, env.StopNamespace(context.Background()))
	require.NoError(t, (&Daemon{}).newDepsEnv(activeNamespace{}).StopNamespace(context.Background()))
}

func TestStopNamespaceHonoursACanceledContext(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt})
	env.stopPoll = time.Millisecond
	env.stopWait = time.Minute
	rt.SetStatusForTest(namespace.NsStatusRunning)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, env.StopNamespace(ctx), context.Canceled)
}

// ReloadAndStart is the daemon's own reload path, and it must take reloadMu —
// and ONLY reloadMu: the migration already holds the long-operation lock for
// its whole duration, so taking it again would deadlock the finalize step.
func TestReloadAndStartTakesReloadMuAndNotTheLongOpLock(t *testing.T) {
	for _, tc := range []struct{ start bool }{{true}, {false}} {
		t.Run(fmt.Sprintf("start=%v", tc.start), func(t *testing.T) {
			d := &Daemon{}
			require.True(t, d.longOp.TryLock(longOpMigration), "the migration holds the lock for its whole run")
			t.Cleanup(d.longOp.Unlock)

			var plainReloads int
			var exReloads [][3]bool
			d.reloadFn = func() error {
				plainReloads++
				assert.False(t, d.reloadMu.TryLock(), "the reload runs under reloadMu")
				return nil
			}
			d.reloadExFn = func(force, startNotRegen, refresh bool) error {
				exReloads = append(exReloads, [3]bool{force, startNotRegen, refresh})
				assert.False(t, d.reloadMu.TryLock(), "the reload runs under reloadMu")
				return nil
			}

			env := d.newDepsEnv(activeNamespace{})
			require.NoError(t, env.ReloadAndStart(context.Background(), tc.start))
			if tc.start {
				assert.Equal(t, [][3]bool{{false, true, false}}, exReloads,
					"a namespace that was running is started from the freshly generated set")
				assert.Zero(t, plainReloads)
			} else {
				assert.Equal(t, 1, plainReloads,
					"a stopped namespace is regenerated so its files follow the new pin")
				assert.Empty(t, exReloads)
			}
			assert.True(t, d.reloadMu.TryLock(), "reloadMu is released again")
			d.reloadMu.Unlock()
		})
	}
}

func TestReloadAndStartSurfacesTheReloadError(t *testing.T) {
	d := &Daemon{}
	d.reloadFn = func() error { return errors.New("resolve bundle: nope") }
	env := d.newDepsEnv(activeNamespace{})
	require.Error(t, env.ReloadAndStart(context.Background(), false))
}

// --- generation ------------------------------------------------------------

// GenerateDefFor runs the namespace's REAL generation with one pin forced, so
// the temp container gets the same Cmd and the same config binds the
// namespace's own postgres has — and it must not write anything: the config
// files it references are already on disk from the last real reload, and the
// runtime's own view of the namespace is not this function's to move.
func TestGenerateDefForForcesThePinAndKeepsTheRealConfig(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	base := t.TempDir()
	act := activeNamespace{
		runtime: rt,
		nsConfig: &namespace.Config{
			ID:             "ns1",
			Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
			Proxy:          namespace.ProxyProps{Port: 80},
		},
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		systemSecrets:   namespace.SystemSecrets{JWT: "j", OIDC: "o"},
		volumesBase:     base,
	}
	env := (&Daemon{}).newDepsEnv(act)

	src, err := env.GenerateDefFor(deps.Postgres, "postgres:17.5")
	require.NoError(t, err)
	assert.Equal(t, "postgres:17.5", src.Image)
	assert.Contains(t, src.Volumes, "postgres2:/var/lib/postgresql/data")
	assert.Contains(t, src.Volumes, "./postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf")
	assert.Equal(t, []string{"-c", "config_file=/etc/postgresql/postgresql.conf"}, src.Cmd)

	dst, err := env.GenerateDefFor(deps.Postgres, "postgres:18")
	require.NoError(t, err)
	assert.Equal(t, "postgres:18", dst.Image)
	assert.Contains(t, dst.Volumes, "postgres3:/var/lib/postgresql")
	assert.Contains(t, dst.Volumes, "./postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf")

	// Nothing was written and nothing in the runtime moved: the forced pin is
	// a local override for ONE generation.
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	assert.Empty(t, entries, "GenerateDefFor must not write runtime files")
	assert.Equal(t, map[deps.ID]string{deps.Postgres: "postgres:17.5"}, rt.DependencyPins(),
		"the runtime's pins are copied, never overwritten")
	assert.Empty(t, rt.AppliedConfig().Authentication.Users,
		"the runtime keeps the config it was driving — generating a def does not apply one")
}

func TestGenerateDefForRefusesWhatItCannotAnswer(t *testing.T) {
	env := (&Daemon{}).newDepsEnv(activeNamespace{})
	_, err := env.GenerateDefFor(deps.Postgres, "postgres:18")
	require.Error(t, err, "no namespace loaded")

	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env = (&Daemon{}).newDepsEnv(activeNamespace{
		runtime:         rt,
		nsConfig:        &namespace.Config{ID: "ns1"},
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		volumesBase:     t.TempDir(),
	})
	_, err = env.GenerateDefFor(deps.ID("nope"), "whatever:1")
	require.Error(t, err, "an unregistered dependency has no app to generate")
}
