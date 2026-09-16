package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
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
	// utilsRuns records every utils invocation in order — command, binds and
	// the wait budget it was given. CopyVolume makes TWO (the copy and the
	// verification that follows it), and the binds of each are the assertion
	// that matters: the source must be mounted READ-ONLY both times.
	utilsRuns []utilsRun
	// utilsQueue is consumed in order when non-empty, so a test can script the
	// copy's answer and the verification's separately.
	utilsQueue []utilsResult

	ensureUtilsErr  error
	ensureUtilsRuns int

	volumes    map[string]*volume.Volume
	volumeErr  error
	volSize    map[string]int64
	removedVol []string

	localImages map[string]bool

	pulled   []string
	pullPct  []int
	pullErr  error
	pullAuth *docker.RegistryAuth
}

func newFakeDepsDocker() *fakeDepsDocker {
	return &fakeDepsDocker{
		newID:       "cid-1",
		inspect:     map[string]container.InspectResponse{},
		inspectErr:  map[string]error{},
		volumes:     map[string]*volume.Volume{},
		volSize:     map[string]int64{},
		localImages: map[string]bool{},
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

func (f *fakeDepsDocker) ImageExists(_ context.Context, img string) bool {
	f.record("imageexists:" + img)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.localImages[img]
}

func (f *fakeDepsDocker) EnsureUtilsImage(context.Context) error {
	f.record("ensureutils")
	f.mu.Lock()
	f.ensureUtilsRuns++
	f.mu.Unlock()
	return f.ensureUtilsErr
}

func (f *fakeDepsDocker) RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error) {
	return f.RunUtilsContainerWithTimeout(ctx, cmd, binds, 5*time.Minute)
}

func (f *fakeDepsDocker) RunUtilsContainerWithTimeout(_ context.Context, cmd, binds []string,
	timeout time.Duration,
) (output string, exitCode int, err error) {
	f.record("utils:" + strings.Join(cmd, " "))
	f.mu.Lock()
	f.utilsBind = binds
	f.utilsRuns = append(f.utilsRuns, utilsRun{cmd: cmd, binds: binds, timeout: timeout})
	if len(f.utilsQueue) > 0 {
		res := f.utilsQueue[0]
		f.utilsQueue = f.utilsQueue[1:]
		f.mu.Unlock()
		return res.out, res.code, res.err
	}
	f.mu.Unlock()
	return f.utilsOut, f.utilsCode, f.utilsErr
}

// utilsRun is one recorded utils invocation.
type utilsRun struct {
	cmd     []string
	binds   []string
	timeout time.Duration
}

// utilsResult is one scripted answer for utilsQueue.
type utilsResult struct {
	out  string
	code int
	err  error
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
	// One assignment, not two: the Env's Docker client IS the probe's (the
	// probe is embedded), which is the point of that shape.
	env.dc = fake
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

	id, err := env.RunAppDef(context.Background(), def, deps.TempContainerOpts{
		Name: migrate.SrcContainer, ExtraBinds: []string{"/host/dump:/citeck/depsmig"},
	})
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
	assert.True(t, got.opts.NoRestart,
		"a temp container must not be restarted by Docker after the launcher is gone: "+
			"it would come back with a migration's data volume mounted and contend with the namespace's own server")
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

	_, err := env.RunAppDef(context.Background(), appdef.ApplicationDef{Name: "postgres"},
		deps.TempContainerOpts{Name: "depsmig-dst"})
	require.Error(t, err)
	assert.Contains(t, fake.Calls(), "rmcontainer:cid-1",
		"a container that would not start must not be left behind")
}

func TestRunAppDefFailsWhenTheNetworkCannotBeEnsured(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.networkErr = errors.New("no network")
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.RunAppDef(context.Background(), appdef.ApplicationDef{Name: "postgres"},
		deps.TempContainerOpts{Name: "depsmig-src"})
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
	// Three bytes of content occupying a whole block: the volume COSTS the
	// block, and the space requirement is what this feeds. See
	// TestVolumeSizeRequiresTheLargerOfApparentAndAllocation for the rule.
	assert.GreaterOrEqual(t, size, int64(3))

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

	fake.utilsQueue = []utilsResult{{out: "bytes 4096\nkb 4"}}
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

// On a server the dump directory and the data volumes are both under the
// namespace's volumes base, so the migration writes both halves — the dump and
// the new cluster, which coexist until the commit — onto ONE filesystem. The
// preflight has to know that to demand the sum instead of one half, and the
// daemon is the only one that can tell it: a volume NAME says nothing about
// where its bytes land.
func TestOnAServerTheDumpAndTheVolumesShareOneFilesystem(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes"), 0o755))

	shared, err := env.DumpSharesFilesystemWithVolumes(context.Background(), "postgres2")
	require.NoError(t, err)
	assert.True(t, shared, "both are directories under the same volumes base")
	assert.Empty(t, fake.Calls(), "the host filesystem answers directly — no container")
}

// A comparison that cannot be made is an ERROR, never a "different
// filesystems": the preflight reads the failure as "require room for both",
// and a guessed answer would silently halve what it demands.
func TestTheServerFilesystemComparisonRefusesToGuess(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	env.act.volumesBase = filepath.Join(t.TempDir(), "not-there")

	_, err := env.DumpSharesFilesystemWithVolumes(context.Background(), "postgres2")
	require.Error(t, err, `an unmeasurable path must not be reported as "two filesystems"`)
}

// On a desktop the answer comes from the OS rule and nothing else — no stat of
// a path the desktop user cannot read, and no engine call: the migration must
// not depend on Docker to decide how much space it needs.
func TestOnADesktopTheAnswerIsTheOSRuleAndCostsNothing(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	env.act.volumesBase = filepath.Join(t.TempDir(), "not-there")

	shared, err := env.DumpSharesFilesystemWithVolumes(context.Background(), "postgres2")
	require.NoError(t, err, "the rule needs no filesystem to answer")
	assert.Equal(t, goruntime.GOOS == "linux", shared)
	assert.Empty(t, fake.Calls())
}

// The desktop rule itself, stated per OS so the choice is testable on any of
// them: the Docker VM's disk on macOS/Windows is certainly a different
// filesystem, while on Linux the engine runs on the same kernel and we cannot
// prove either way — so that one takes the safe direction.
func TestTheDesktopFilesystemRuleIsPerOS(t *testing.T) {
	assert.False(t, desktopSharesHostFilesystem("darwin"), "the volumes live in the Docker VM")
	assert.False(t, desktopSharesHostFilesystem("windows"), "the volumes live in the Docker VM")
	assert.True(t, desktopSharesHostFilesystem("linux"), "cannot be proven; require room for both")
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
		// The bundle names the image, so the CANDIDATE is this test's own
		// value. The pin gate emits a forced pin verbatim only when it is
		// breaking against the candidate (17.5 against an 18) or equal to it,
		// so riding on the generator's FALLBACK instead makes these assertions
		// fail the day that default moves — which is what happened when it
		// went from postgres:18 to postgres:18.6.
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: testTargetPgImage}}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		systemSecrets:   namespace.SystemSecrets{JWT: "j", OIDC: "o"},
		volumesBase:     base,
	}
	env := (&Daemon{}).newDepsEnv(act)

	src, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
	require.NoError(t, err)
	assert.Equal(t, "postgres:17.5", src.Image)
	assert.Contains(t, src.Volumes, "postgres2:/var/lib/postgresql/data")
	assert.Contains(t, src.Volumes, "./postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf")
	assert.Equal(t, []string{"-c", "config_file=/etc/postgresql/postgresql.conf"}, src.Cmd)

	dst, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: testTargetPgImage, VolumeGen: 2})
	require.NoError(t, err)
	assert.Equal(t, testTargetPgImage, dst.Image)
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

// GenerateDefFor's "it writes NOTHING" contract rests on a claim about the
// generated def: the files a temp container binds are the ones already on disk
// from the last real reload, because they do not depend on the dependency's
// VERSION. That claim is only true while the source def and the target def
// bind exactly the same non-data files — the moment a major moves one of them
// (a version-specific postgresql.conf, say), a temp container would mount a
// path nobody wrote and the migration would fail on a stopped namespace, with
// the cause two layers away from the symptom.
//
// So it is checked directly: same config binds, and — the other half, without
// which the comparison could pass vacuously — DIFFERENT data volumes, which is
// the plan's own precondition (the new cluster is built beside the old data,
// and the rollback is a deletion).
func TestGenerateDefForGivesBothMajorsTheSameNonDataBinds(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	env := (&Daemon{}).newDepsEnv(activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "ns1", Proxy: namespace.ProxyProps{Port: 80}},
		// The bundle names the image, so the CANDIDATE is this test's own
		// value. The pin gate emits a forced pin verbatim only when it is
		// breaking against the candidate (17.5 against an 18) or equal to it,
		// so riding on the generator's FALLBACK instead makes these assertions
		// fail the day that default moves — which is what happened when it
		// went from postgres:18 to postgres:18.6.
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: testTargetPgImage}}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		volumesBase:     t.TempDir(),
	})

	src, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
	require.NoError(t, err)
	dst, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: testTargetPgImage, VolumeGen: 2})
	require.NoError(t, err)

	srcData, srcFiles := splitDataAndFileBinds(src.Volumes)
	dstData, dstFiles := splitDataAndFileBinds(dst.Volumes)

	require.NotEmpty(t, srcFiles, "postgres binds its config files — this guard is out of date without them")
	assert.Equal(t, srcFiles, dstFiles,
		"both majors must bind the same files: GenerateDefFor writes none of them, "+
			"so a file only one version asks for would be mounted from a path that does not exist")
	require.NotEmpty(t, srcData)
	assert.NotEqual(t, srcData, dstData,
		"the two majors must land in DIFFERENT volumes — the migration builds the new cluster beside the old data")
}

// splitDataAndFileBinds separates a def's volume list into the DATA mounts
// (plain volume names, which is what moves between majors) and the file/dir
// binds (relative or absolute host paths), each sorted so the comparison does
// not depend on generator order.
func splitDataAndFileBinds(vols []string) (data, files []string) {
	for _, v := range vols {
		src, _, _ := strings.Cut(v, ":")
		if strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") {
			files = append(files, v)
			continue
		}
		data = append(data, v)
	}
	slices.Sort(data)
	slices.Sort(files)
	return data, files
}

func TestGenerateDefForRefusesWhatItCannotAnswer(t *testing.T) {
	env := (&Daemon{}).newDepsEnv(activeNamespace{})
	_, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: testTargetPgImage, VolumeGen: 2})
	require.Error(t, err, "no namespace loaded")

	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	env = (&Daemon{}).newDepsEnv(activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "ns1"},
		// The bundle names the image, so the CANDIDATE is this test's own
		// value. The pin gate emits a forced pin verbatim only when it is
		// breaking against the candidate (17.5 against an 18) or equal to it,
		// so riding on the generator's FALLBACK instead makes these assertions
		// fail the day that default moves — which is what happened when it
		// went from postgres:18 to postgres:18.6.
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: testTargetPgImage}}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		volumesBase:     t.TempDir(),
	})
	_, err = env.GenerateDefFor(deps.ID("nope"), deps.DependencyState{Image: "whatever:1"})
	require.Error(t, err, "an unregistered dependency has no app to generate")
}

// The forced pin survives the generation only because the plan asks for a
// BREAKING version, which the pin gate emits verbatim. A non-breaking one is
// resolved to the bundle's candidate instead, and the caller would silently get
// a temp container running an image it did not name — started on somebody's
// data, under a name that lies about it. It is refused rather than returned.
func TestGenerateDefForRefusesADefTheGeneratorRehomed(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	env := (&Daemon{}).newDepsEnv(activeNamespace{
		runtime: rt,
		nsConfig: &namespace.Config{
			ID:             "ns1",
			Authentication: namespace.AuthenticationProps{Type: namespace.AuthBasic, Users: []string{"admin"}},
			Proxy:          namespace.ProxyProps{Port: 80},
		},
		// The bundle offers the same major from a mirror, so the gate rehomes
		// the pin — the def comes back on the mirror, not on what was asked for.
		bundleDef: &bundle.Def{Applications: map[string]bundle.AppDef{
			appdef.AppPostgres: {Image: "mirror.example.com/postgres:18"},
		}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		systemSecrets:   namespace.SystemSecrets{JWT: "j", OIDC: "o"},
		volumesBase:     t.TempDir(),
	})
	_, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: "postgres:17.5"})
	require.ErrorContains(t, err, "not to the requested")
	require.ErrorContains(t, err, "mirror.example.com/postgres:17.5")
}

// ReloadAndStart reloads whatever namespace is ACTIVE, so an Env built for a
// different one must refuse instead. Two callers make that reachable: crash
// recovery, whose Env describes a namespace that is not installed yet, and a
// namespace switch racing a migration. Without the guard the recovery of a
// namespace nobody activated would start a STRANGER.
func TestReloadAndStartRefusesANamespaceThatIsNotActive(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{nsConfig: &namespace.Config{ID: "active-ns"}}}
	var reloads int
	d.reloadFn = func() error { reloads++; return nil }
	d.reloadExFn = func(bool, bool, bool) error { reloads++; return nil }

	env := d.newDepsEnv(activeNamespace{nsConfig: &namespace.Config{ID: "other-ns"}})
	err := env.ReloadAndStart(context.Background(), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "other-ns")
	assert.Contains(t, err.Error(), "active-ns")
	assert.Zero(t, reloads, "the wrong namespace must not be reloaded at all")

	// The migration's own namespace is served as before.
	env = d.newDepsEnv(activeNamespace{nsConfig: &namespace.Config{ID: "active-ns"}})
	require.NoError(t, env.ReloadAndStart(context.Background(), true))
	assert.Equal(t, 1, reloads)
}

// --- the temp container's node identity -------------------------------------

// RunAppDef's opts carry the two things a generated def cannot: extra
// environment and container-local /etc/hosts entries. Both exist for one
// measured reason, and they are asserted TOGETHER because either half alone is
// useless.
//
// RabbitMQ derives its node name from the container hostname and its data
// directory contains that node name, so a temp container running under the
// override name boots a fresh EMPTY node inside the copy and reports healthy
// (measured: `list_users` then showed `guest` alone and a second
// `rabbit@depsmig-src` directory appeared beside the real one). Pinning the
// identity needs RABBITMQ_NODENAME *and* an /etc/hosts entry for its host
// part: the environment alone fails the boot outright with
//
//	ERROR: epmd error for host rabbitmq: nxdomain (non-existing domain)
//
// so dropping the alias to "simplify" it is a migration that dies at step 5
// with an error about DNS.
//
// The other half of the same decision is what must NOT change: the container's
// hostname and its network aliases stay the TEMP name. /etc/hosts is
// container-local and invisible to Docker's DNS, which is exactly why it was
// chosen over a hostname override — moby registers a hostname as a DNS name on
// a user-defined network, and a temp container answering to "rabbitmq" there
// is the hazard the override exists to prevent.
func TestRunAppDefStillStripsPortsAndAliasesAndNowCarriesTheExtraEnv(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)

	def := appdef.ApplicationDef{
		Name:           "rabbitmq",
		Image:          "rabbitmq:4.1.2-management",
		Ports:          []string{"5672:5672"},
		NetworkAliases: []string{"rabbitmq", "amqp"},
		Environments:   appdef.OrderedMap{{Key: "RABBITMQ_DEFAULT_USER", Value: "citeck"}},
	}
	_, err := env.RunAppDef(context.Background(), def, deps.TempContainerOpts{
		Name:      migrate.SrcContainer,
		Env:       map[string]string{"RABBITMQ_NODENAME": "rabbit@rabbitmq"},
		HostAlias: map[string]string{"rabbitmq": "127.0.0.1"},
	})
	require.NoError(t, err)

	require.Len(t, fake.created, 1)
	got := fake.created[0]
	assert.Nil(t, got.def.Ports, "published ports are stripped")
	assert.Nil(t, got.def.NetworkAliases, "the temp container answers only to its own name")
	assert.Equal(t, appdef.OrderedMap{
		{Key: "RABBITMQ_DEFAULT_USER", Value: "citeck"},
		{Key: "RABBITMQ_NODENAME", Value: "rabbit@rabbitmq"},
	}, got.def.Environments, "the def's own environment is kept and the node name added to it")
	assert.Equal(t, []string{"rabbitmq:127.0.0.1"}, got.opts.ExtraHosts,
		"without the alias the broker refuses to boot: epmd error for host rabbitmq: nxdomain")
	assert.Equal(t, migrate.SrcContainer, got.opts.Name,
		"the DNS identity stays the temp one — the node identity is the env's job, not the hostname's")

	// The caller's def is untouched: the extra environment lands on a COPY.
	// A def is usually a struct copy sharing one backing array with the
	// runtime's own, so appending in place would write the temp container's
	// node name into the namespace's real app def.
	assert.Equal(t, appdef.OrderedMap{{Key: "RABBITMQ_DEFAULT_USER", Value: "citeck"}}, def.Environments)
	assert.Equal(t, []string{"rabbitmq", "amqp"}, def.NetworkAliases)
}

// An override that repeats a key the def already sets must UPDATE it, not
// append a second entry: an image reads the last occurrence, so two entries
// would leave the def and the container disagreeing about what is running.
func TestRunAppDefOverridesAnEnvironmentKeyTheDefAlreadyHas(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.RunAppDef(context.Background(), appdef.ApplicationDef{
		Name:         "rabbitmq",
		Environments: appdef.OrderedMap{{Key: "RABBITMQ_NODENAME", Value: "rabbit@somewhere"}},
	}, deps.TempContainerOpts{
		Name: migrate.SrcContainer,
		Env:  map[string]string{"RABBITMQ_NODENAME": "rabbit@rabbitmq"},
	})
	require.NoError(t, err)
	assert.Equal(t, appdef.OrderedMap{{Key: "RABBITMQ_NODENAME", Value: "rabbit@rabbitmq"}},
		fake.created[0].def.Environments)
}

// --- copying a volume -------------------------------------------------------

// TestCopyVolumeMountsTheSourceReadOnly is the structural half of "the
// namespace's own data volume is only ever READ".
//
// A copy-upgrade plan's whole safety argument is that its rollback — "delete
// the volume we made" — is a complete undo, and that is only true while
// nothing can write to the source. The `:ro` on the source bind is what makes
// it true at the Docker level rather than by convention, and it must be there
// on BOTH utils runs: the copy and the verification that follows it.
func TestCopyVolumeMountsTheSourceReadOnly(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{
		{out: ""},
		{out: "srcfiles 12\nsrcbytes 491520\ndstfiles 12\ndstbytes 491520\n"},
	}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "rabbitmq2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "rabbitmq3"), 0o755))

	require.NoError(t, env.CopyVolume(context.Background(), "rabbitmq2", "rabbitmq3"))

	require.Len(t, fake.utilsRuns, 2, "the copy and its verification")
	src := filepath.Join(base, "volumes", "rabbitmq2")
	dst := filepath.Join(base, "volumes", "rabbitmq3")
	assert.Equal(t, []string{src + ":/src:ro", dst + ":/dst"}, fake.utilsRuns[0].binds,
		"the source is read-only; the copy writes only into the volume the plan created")
	assert.Equal(t, []string{src + ":/src:ro", dst + ":/dst:ro"}, fake.utilsRuns[1].binds,
		"the verification reads both sides and writes to neither")
}

// The copy preserves ownership, mode and mtimes, which is not hygiene: the
// data is owned by the image's uid (rabbitmq 999, zookeeper 1000, postgres
// 999), and a .erlang.cookie that ends up owned by root fails RabbitMQ's boot
// with "eacces" → "Kernel pid terminated" (measured). `tar -cf - | tar -xpf -`
// is the mechanism, run as root inside the utils container, which is what lets
// it restore an owner the daemon may not even be allowed to name.
func TestCopyVolumeUsesAnOwnershipPreservingCopy(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{{}, {out: "srcfiles 1\nsrcbytes 1\ndstfiles 1\ndstbytes 1"}}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3"), 0o755))

	require.NoError(t, env.CopyVolume(context.Background(), "postgres2", "postgres3"))

	cmd := strings.Join(fake.utilsRuns[0].cmd, " ")
	assert.Contains(t, cmd, "tar -C /src -Scf - .")
	assert.Contains(t, cmd, "tar -C /dst -Sxpf -",
		"-p is what restores the mode; without it a 0400 .erlang.cookie comes back 0644")
}

// A copy is minutes to hours. The default utils budget is five minutes, which
// is right for a `cat` and would make a real copy look exactly like a copy
// that FAILED — on a namespace the migration has already stopped.
func TestCopyVolumeIsNotBoundedByTheShortProbeTimeout(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{{}, {out: "srcfiles 0\nsrcbytes 0\ndstfiles 0\ndstbytes 0"}}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3"), 0o755))

	require.NoError(t, env.CopyVolume(context.Background(), "postgres2", "postgres3"))
	assert.GreaterOrEqual(t, fake.utilsRuns[0].timeout, time.Hour,
		"a data volume copy needs hours, not the five minutes a `cat` gets")
}

// The verification is what turns a partial copy — a broken pipe, a device that
// filled up mid-stream — into a failed step instead of a container started on
// half a data directory.
func TestCopyVolumeFailsWhenTheCopyDoesNotMatchTheSource(t *testing.T) {
	// The two arms are exercised SEPARATELY. Together in one fixture the file
	// count alone catches everything, and the byte comparison could be deleted
	// with the test still green — which is the arm that matters most, because
	// the failure it exists for (a stream cut short by a broken pipe or an
	// ENOSPC) can leave the file COUNT intact and only the content short.
	cases := map[string]struct {
		measure string
		want    []string
	}{
		"a file went missing": {
			measure: "srcfiles 120\nsrcbytes 4194304\ndstfiles 118\ndstbytes 4194304",
			want:    []string{"118 files", "120 files"},
		},
		"the same files, cut short": {
			measure: "srcfiles 120\nsrcbytes 4194304\ndstfiles 120\ndstbytes 4096000",
			want:    []string{"4096000 bytes", "4194304 bytes"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeDepsDocker()
			fake.utilsQueue = []utilsResult{{}, {out: c.measure}}
			env, base := newTestDepsEnv(t, fake)
			require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres2"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3"), 0o755))

			err := env.CopyVolume(context.Background(), "postgres2", "postgres3")
			require.Error(t, err)
			for _, w := range c.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

// A measurement that did not happen must not compare equal to another one that
// did not: four missing numbers are not four matching zeroes.
func TestCopyVolumeFailsWhenTheMeasurementIsUnreadable(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{{}, {out: "find: /src: Permission denied\n"}}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3"), 0o755))

	err := env.CopyVolume(context.Background(), "postgres2", "postgres3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "srcfiles")
}

// An absent SOURCE volume is refused rather than auto-created by Docker: an
// empty copy is a faithful copy of nothing, and for RabbitMQ it boots as a
// brand-new node and reports healthy.
func TestCopyVolumeRefusesAnAbsentSource(t *testing.T) {
	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "postgres3"), 0o755))

	err := env.CopyVolume(context.Background(), "postgres2", "postgres3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres2")
	assert.Empty(t, fake.utilsRuns, "nothing may be copied when the source could not be resolved")
}

// EnsureVolumeDirs exists because a temp container runs the CONTAINER and
// nothing around it — no init actions, no probes, and no INIT CONTAINERS.
// ZooKeeper's generated def has an init container whose only job is
// `mkdir -p /zkdir/data /zkdir/datalog`, so a copy of a volume that has never
// held a running ZooKeeper would start the temp container against directories
// that are not there. It runs through the utils container so they end up owned
// by ROOT, which is what the real init container produces too.
func TestEnsureVolumeDirsCreatesThemInsideTheVolume(t *testing.T) {
	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "zookeeper3"), 0o755))

	require.NoError(t, env.EnsureVolumeDirs(context.Background(), "zookeeper3", []string{"data", "datalog"}))

	require.Len(t, fake.utilsRuns, 1)
	assert.Equal(t, []string{"mkdir", "-p", "/dst/data", "/dst/datalog"}, fake.utilsRuns[0].cmd)
	assert.Equal(t, []string{filepath.Join(base, "volumes", "zookeeper3") + ":/dst"}, fake.utilsRuns[0].binds)
}

// The utils container runs as ROOT, so a path that escapes the mount would
// have it create directories somewhere else on the host entirely. The callers
// are the launcher's own CopySpecs, so this is a typo guard rather than a
// trust boundary — which is exactly why it must refuse rather than clean.
func TestEnsureVolumeDirsRefusesAPathThatLeavesTheVolume(t *testing.T) {
	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "zookeeper3"), 0o755))

	err := env.EnsureVolumeDirs(context.Background(), "zookeeper3", []string{"../../etc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "leaves the volume")
	assert.Empty(t, fake.utilsRuns)
}

// Nothing to create is not a reason to start a container.
func TestEnsureVolumeDirsWithNothingToDoRunsNothing(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	require.NoError(t, env.EnsureVolumeDirs(context.Background(), "zookeeper3", nil))
	assert.Empty(t, fake.Calls())
}

// DependencyState is the pin the plan reads BOTH halves of: the image the data
// runs on and which generation of the volume holds it. An unpinned dependency
// answers the zero value, whose Gen() is 1 — the volume every namespace that
// has never migrated runs.
func TestDependencyStateAnswersTheWholePin(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:18", VolumeGen: 2},
	}, nil, nil)
	env := (&Daemon{}).newDepsEnv(activeNamespace{runtime: rt, nsConfig: &namespace.Config{ID: "ns1"}})

	assert.Equal(t, deps.DependencyState{Image: "postgres:18", VolumeGen: 2}, env.DependencyState(deps.Postgres))
	assert.Equal(t, 1, env.DependencyState(deps.RabbitMQ).Gen(), "an unpinned dependency is generation 1")

	noRuntime := (&Daemon{}).newDepsEnv(activeNamespace{})
	assert.Equal(t, deps.DependencyState{}, noRuntime.DependencyState(deps.Postgres))
}

// GenerateDefFor guarantees BOTH halves of the pin it was asked for, and the
// volume half is the one a copy-upgrade plan rests on: every container it
// starts must land on the COPY. A def that mounted the SOURCE volume instead
// would run the old image, the new image and the whole pre/post upgrade
// sequence against the namespace's real data — the one thing the plan promises
// never to touch — and nothing downstream would notice, because the container
// would be perfectly healthy and the data perfectly real.
func TestGenerateDefForRefusesADefThatDoesNotMountTheRequestedVolume(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	env := (&Daemon{}).newDepsEnv(activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "ns1", Proxy: namespace.ProxyProps{Port: 80}},
		// The bundle names the image so the CANDIDATE is this test's own value
		// — see testTargetPgImage.
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: testTargetPgImage}}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		volumesBase:     t.TempDir(),
	})

	// Generation 2 is what the generator emits for a generation-2 pin, so this
	// one is served.
	dst, err := env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: testTargetPgImage, VolumeGen: 2})
	require.NoError(t, err)
	assert.Contains(t, dst.Volumes, "postgres3:/var/lib/postgresql")

	// The reachable way for the def to come back on another volume is a
	// per-app EDIT. DEPENDENCY_VERSION_LOCKED guards the IMAGE and deliberately
	// nothing else, so a patch that rewrites `volumes:` is accepted as
	// legitimate operator surgery — and patches land at the TAIL of Generate,
	// after the pin gate. A temp container built from such a def would run the
	// old image, the new image and the whole upgrade sequence against whatever
	// that patch names, which for a copy-upgrade plan may be the SOURCE volume
	// the plan promises never to touch.
	rt.RestoreEditedState(map[string]json.RawMessage{
		appdef.AppPostgres: json.RawMessage(`{"volumes":["postgres2:/var/lib/postgresql"]}`),
	}, nil)
	_, err = env.GenerateDefFor(deps.Postgres, deps.DependencyState{Image: testTargetPgImage, VolumeGen: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres3", "the message names the volume that was asked for")
	assert.Contains(t, err.Error(), "postgres2", "...and the ones it actually got")
}

// newPostgresLadderEnv is the harness the multi-rung PostgreSQL walk's
// GenerateDefForVolume calls run against: a namespace pinned to postgres:17.5
// generation 1 (so deps.VolumeName(d, st.Gen()) — "ordinary" — is postgres2
// for every call below, exactly as it is for the real plan's intermediate
// rungs, which always pass a DependencyState with VolumeGen left at its zero
// value; see the comment at the pgRun.startTempOnVolume call site).
func newPostgresLadderEnv(t *testing.T, volumesPatch string) *depsEnv {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, nil, nil)
	if volumesPatch != "" {
		rt.RestoreEditedState(map[string]json.RawMessage{
			appdef.AppPostgres: json.RawMessage(volumesPatch),
		}, nil)
	}
	env := (&Daemon{}).newDepsEnv(activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "ns1", Proxy: namespace.ProxyProps{Port: 80}},
		// The bundle names the SAME image as the pin, so the candidate is not
		// breaking and the gate would otherwise resolve it away from what
		// these tests ask GenerateDefForVolume for — a multi-rung walk's
		// intermediate rungs pass exactly this shape of DependencyState
		// (Image set, VolumeGen left at its zero value).
		bundleDef:       &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:17.5"}}},
		workspaceConfig: &bundle.WorkspaceConfig{},
		volumesBase:     t.TempDir(),
	})
	return env
}

// GenerateDefForVolume is the mechanism the whole "the source is only ever
// read" invariant of a multi-rung PostgreSQL walk rests on: every
// intermediate rung's container is generated through it rather than through
// GenerateDefFor, precisely because the scratch volume has no generation of
// its own. This is its dedicated coverage — GenerateDefFor's own tests above
// exercise the ordinary, single-volume path and never call this method at
// all.
func TestGenerateDefForVolumeMountsTheScratchVolumeNotAGeneration(t *testing.T) {
	env := newPostgresLadderEnv(t, "")

	def, err := env.GenerateDefForVolume(deps.Postgres, deps.DependencyState{Image: "postgres:18.6"}, "postgres3-hop")
	require.NoError(t, err)
	assert.Equal(t, "postgres:18.6", def.Image)
	assert.Contains(t, def.Volumes, "postgres3-hop:/var/lib/postgresql",
		"the scratch volume, not postgres2 (the source generation) or postgres3 (the final generation)")
	assert.NotContains(t, strings.Join(def.Volumes, "\n"), "postgres2:",
		"the generation's OWN volume must not appear anywhere in the returned def")
	assert.NotContains(t, strings.Join(def.Volumes, "\n"), "postgres3:",
		"nor the final generation's — this call names neither")
}

// The generator's own mount is checked BEFORE any retargeting is attempted:
// if it does not carry the volume ordinary names, retargetVolume would have
// nothing correct to rewrite FROM, and proceeding would silently hand back a
// def whose data mount is whatever the patch put there — never verified
// against anything.
func TestGenerateDefForVolumeRefusesWhenTheGeneratorsOwnMountDoesNotMatchOrdinary(t *testing.T) {
	env := newPostgresLadderEnv(t, `{"volumes":["somewhere-else:/var/lib/postgresql/data"]}`)

	_, err := env.GenerateDefForVolume(deps.Postgres, deps.DependencyState{Image: "postgres:18.6"}, "postgres3-hop")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres2", "names ordinary — what it expected to find and retarget FROM")
	assert.Contains(t, err.Error(), "not the expected")
}

// retargetVolume rewrites EVERY entry whose source is ordinary, not merely
// the first — verified directly here rather than trusted, because a def with
// two binds to the namespace's real data (a second bind for a PGDATA
// subdirectory, a WAL-archive mount, anything a bundle adds) must come back
// with NEITHER one still pointing at it. GenerateDefForVolume proves this
// itself, after retargeting, rather than resting on retargetVolume's own
// correctness: see the postcondition check right after the a.Volumes
// assignment in GenerateDefForVolume. Weakening retargetVolume to stop after
// its first match — a realistic future "optimization" of a loop that looks
// like it only ever needs to fire once — is exactly the regression that
// postcondition exists to catch, and it is what running this test against
// such a mutation refuses on (verified by hand; see the task report).
func TestGenerateDefForVolumeLeavesNoBindToTheSourceAfterRetargetingTwoEntries(t *testing.T) {
	env := newPostgresLadderEnv(t, `{"volumes":[
		"postgres2:/var/lib/postgresql/data",
		"postgres2:/var/lib/postgresql/data/pg_wal"
	]}`)

	def, err := env.GenerateDefForVolume(deps.Postgres, deps.DependencyState{Image: "postgres:18.6"}, "postgres3-hop")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"postgres3-hop:/var/lib/postgresql/data",
		"postgres3-hop:/var/lib/postgresql/data/pg_wal",
	}, def.Volumes, "BOTH binds to the source must be retargeted, not just one")
	assert.False(t, mountsVolume(def, "postgres2"),
		"the def must not still mount the source under any bind after retargeting")
}

// A temp container with no name of its own would be created as the
// NAMESPACE'S own container: docker.CreateContainerWith reads "" as "no
// override" and builds the app's container — same name, same LabelAppName,
// adopted by the reconciler — running on whatever volume the plan handed it.
// Its own collision guard cannot catch this, because it refuses an override
// EQUAL to the app's name and "" is not an override at all.
func TestRunAppDefRefusesATempContainerWithNoNameOfItsOwn(t *testing.T) {
	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)

	_, err := env.RunAppDef(context.Background(),
		appdef.ApplicationDef{Name: "postgres", Image: "postgres:17.5"}, deps.TempContainerOpts{})
	require.Error(t, err)
	assert.NotContains(t, fake.Calls(), "create::postgres:17.5",
		"nothing may be created under the namespace's own app name")
	assert.Empty(t, fake.created)
}

// ImageExists is the rollback preflight's heads-up that the image it is about
// to need may have to be pulled — a WARNING, never a refusal, because without
// it that failure lands after the namespace has already been stopped.
//
// It answers a plain bool because nothing downstream could act on the
// difference between "not here" and "I could not ask": docker.Client's own
// ImageExists is an inspect-or-false, and a daemon with NO Docker client is the
// same answer again. Both are "not known to be here", which is the honest input
// to a warning.
func TestImageExistsAnswersFromTheLocalStoreAndNeverRefuses(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.localImages["postgres:17.5"] = true
	env, _ := newTestDepsEnv(t, fake)

	assert.True(t, env.ImageExists(context.Background(), "postgres:17.5"))
	assert.False(t, env.ImageExists(context.Background(), "postgres:18.6"),
		"an image the host does not have is not known to be here")

	noDocker := (&Daemon{}).newDepsEnv(activeNamespace{nsConfig: &namespace.Config{ID: "ns1"}})
	assert.False(t, noDocker.ImageExists(context.Background(), "postgres:17.5"),
		"no engine to ask is the same answer, and it must not panic on a nil client")
}

// testTargetPgImage is the postgres image the GenerateDefFor fixtures put in
// their BUNDLE and then ask for. It is a literal of the test's own rather than
// the generator's fallback: those tests are about the pin gate emitting what
// it was asked for, not about which 18 patch this release defaults to.
const testTargetPgImage = "postgres:18.2"

// TestCopyVolumeAcceptsACopyWhoseBLOCKSDifferFromTheSource is the regression a
// real RabbitMQ copy-upgrade found: the verification compared `du -sk`, which
// is BLOCK ALLOCATION, and failed EVERY migration at the copy-volume step with
//
//	the copy of rabbitmq2 into rabbitmq3 does not match the source:
//	36 files / 252 KiB against 36 files / 256 KiB
//
// Measured on those volumes: identical file count, identical apparent size
// (76972 bytes both sides), and mnesia's schema.DAT = 22093 bytes in 56*512
// blocks on the source against the same 22093 bytes in 48*512 on the copy.
// Mnesia preallocates past EOF; `tar -cf - | tar -xpf -` reproduces the CONTENT
// exactly and is entitled to allocate a different number of blocks for it. So a
// faithful copy was reported as partial and the migration rolled back.
//
// Allocation is not the question the step is asking. What it must catch is a
// copy that lost DATA — a broken pipe, an ENOSPC — and both of those leave
// fewer files or fewer bytes, which apparent size sees.
func TestCopyVolumeAcceptsACopyWhoseBLOCKSDifferFromTheSource(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{
		{},
		// Same files, same bytes. The block counts behind them differ, and the
		// measurement no longer looks at those at all.
		{out: "srcfiles 36\nsrcbytes 76972\ndstfiles 36\ndstbytes 76972"},
	}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "rabbitmq2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "rabbitmq3"), 0o755))

	require.NoError(t, env.CopyVolume(context.Background(), "rabbitmq2", "rabbitmq3"),
		"a copy with identical content must pass however its blocks were allocated")
}

// The measurement must be of APPARENT size, and it must be the sum over regular
// files. `du --apparent-size` would have been the obvious spelling and is not
// available: the utils image's du is busybox and rejects it (verified by
// running it). `stat -c %s` is present there and is what this uses.
func TestTheCopyMeasurementAsksForApparentSizeNotAllocation(t *testing.T) {
	assert.Contains(t, volumeMeasureScript, "stat -c %s",
		"apparent size per file; busybox du has no --apparent-size")
	assert.NotContains(t, volumeMeasureScript, "du -sk",
		"block allocation is what the Mnesia regression was about — it must not come back")
	for _, label := range []string{"srcfiles", "srcbytes", "dstfiles", "dstbytes"} {
		assert.Contains(t, volumeMeasureScript, label)
	}
}

// --- what a volume COSTS ----------------------------------------------------

// TestVolumeSizeRequiresTheLargerOfApparentAndAllocation is the space half of
// the sparse-file problem, and it is a preflight correctness rule: the number
// this returns is what copySpace turns into "you need N bytes free", and
// under-requiring means an ENOSPC in the middle of a migration on an ALREADY
// STOPPED namespace — the exact failure the free-space work exists to prevent.
//
// Neither reading is an upper bound on its own, and both directions were
// measured on real volumes in the launcher-utils image:
//
//   - allocation UNDER-reads a sparse source. ZooKeeper preallocates its
//     txnlog to 64 MiB; `log.1` measured 67108880 bytes apparent in 16 KiB of
//     blocks, and a plain `tar -cf - | tar -xpf -` of it landed as 65556 KiB on
//     the destination. Requiring 16 KiB for a copy that needs 64 MiB is the
//     ENOSPC above, 4096x over.
//   - apparent UNDER-reads dense data, because a filesystem rounds to blocks
//     and honors preallocation past EOF: a 22093-byte mnesia schema.DAT
//     occupied 28672 bytes, and the small-file case in this test is the same
//     effect at its most ordinary.
//
// So the requirement is the larger, in both directions. Over-requiring refuses
// a migration that would have fit and says exactly what it wanted; that is the
// direction this codebase already chose for DumpSharesFilesystemWithVolumes.
func TestVolumeSizeRequiresTheLargerOfApparentAndAllocation(t *testing.T) {
	config.SetDesktopMode(false)
	t.Cleanup(config.ResetDesktopMode)
	fake := newFakeDepsDocker()
	env, base := newTestDepsEnv(t, fake)
	ctx := context.Background()

	// A sparse file, exactly the shape of a ZooKeeper txnlog: 64 MiB of
	// apparent size over almost no blocks. Allocation would answer ~0 here.
	sparseDir := filepath.Join(base, "volumes", "zookeeper2")
	require.NoError(t, os.MkdirAll(sparseDir, 0o755))
	f, err := os.Create(filepath.Join(sparseDir, "log.1"))
	require.NoError(t, err)
	require.NoError(t, f.Truncate(64<<20))
	require.NoError(t, f.Close())

	size, err := env.VolumeSize(ctx, "zookeeper2")
	require.NoError(t, err)
	assert.Equal(t, int64(64<<20), size,
		"a sparse source must be required at its APPARENT size: a plain tar copy writes the holes out")

	// The other direction: a file far smaller than one block. Apparent size
	// would under-read what it actually occupies.
	denseDir := filepath.Join(base, "volumes", "rabbitmq2")
	require.NoError(t, os.MkdirAll(denseDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(denseDir, ".erlang.cookie"), []byte("ABCDE"), 0o400))

	size, err = env.VolumeSize(ctx, "rabbitmq2")
	require.NoError(t, err)
	assert.Greater(t, size, int64(5),
		"five bytes occupy a whole block; the requirement must not be the five")
}

// The two modes must not answer differently about the same data. Server mode
// walked the tree and summed apparent size while desktop ran `du -sk` and got
// allocation, so one namespace's preflight demanded 64 MiB for a ZooKeeper
// txnlog and the other demanded 16 KiB. The rule is one rule.
func TestDesktopVolumeSizeAlsoTakesTheLargerOfTheTwoReadings(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	ctx := context.Background()
	require.NoError(t, env.CreateVolume(ctx, "zookeeper2"))

	// A sparse tree: 64 MiB apparent over 16 KiB of blocks.
	fake.utilsQueue = []utilsResult{{out: "bytes 67108880\nkb 16"}}
	size, err := env.VolumeSize(ctx, "zookeeper2")
	require.NoError(t, err)
	assert.Equal(t, int64(67108880), size, "apparent wins when the data is sparse")

	// A dense tree: the blocks are the larger number.
	fake.utilsQueue = []utilsResult{{out: "bytes 22093\nkb 28"}}
	size, err = env.VolumeSize(ctx, "zookeeper2")
	require.NoError(t, err)
	assert.Equal(t, int64(28*1024), size, "allocation wins when the data is dense")

	// One container for both numbers, and the volume is mounted READ-ONLY:
	// measuring must never be able to change what it measures.
	require.NotEmpty(t, fake.utilsRuns)
	assert.Equal(t, []string{"citeck_volume_zookeeper2:/vol:ro"}, fake.utilsRuns[0].binds)
}

// A measurement that did not happen must not read as a volume of size zero:
// zero is what the preflight prints as "no data", and it would sail through
// the space check it exists to fail.
func TestDesktopVolumeSizeRefusesAnUnreadableMeasurement(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)

	fake := newFakeDepsDocker()
	env, _ := newTestDepsEnv(t, fake)
	ctx := context.Background()
	require.NoError(t, env.CreateVolume(ctx, "zookeeper2"))

	fake.utilsQueue = []utilsResult{{out: "du: /vol: Permission denied"}}
	_, err := env.VolumeSize(ctx, "zookeeper2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bytes")
}

// The copy preserves sparseness. GNU tar's -S is what does it, and the utils
// image ships GNU tar 1.35 (verified by running `tar --version` in it).
// Measured on a 64 MiB ZooKeeper-shaped txnlog: plain tar landed it in 65556
// KiB of blocks, `tar -Scf - | tar -Sxpf -` landed the same 67108885 apparent
// bytes in 16 KiB — and on dense data the two are byte-identical, so it costs
// nothing where there are no holes.
//
// It is an efficiency, NOT a license to require less: the preflight still asks
// for the larger reading, because whether the holes survive depends on the
// DESTINATION filesystem and this launcher cannot see what that is.
func TestTheCopyPreservesSparseFiles(t *testing.T) {
	fake := newFakeDepsDocker()
	fake.utilsQueue = []utilsResult{{}, {out: "srcfiles 1\nsrcbytes 1\ndstfiles 1\ndstbytes 1"}}
	env, base := newTestDepsEnv(t, fake)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "zookeeper2"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "volumes", "zookeeper3"), 0o755))

	require.NoError(t, env.CopyVolume(context.Background(), "zookeeper2", "zookeeper3"))

	cmd := strings.Join(fake.utilsRuns[0].cmd, " ")
	assert.Contains(t, cmd, "-Scf -", "the writer must encode holes instead of reading them as zeros")
	assert.Contains(t, cmd, "-Sxpf -", "and the reader must restore them as holes")
}
