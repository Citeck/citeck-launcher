// Package migratetest provides the in-memory migrate.Env every test that
// exercises a migration plan against a fake world uses: the plan tests and the
// daemon's crash-recovery tests share ONE fake, so a change to the Env seam is
// felt in one place instead of two. The integration test (build tag
// integration) deliberately does not come here — it drives the daemon's real
// Env against real PostgreSQL containers.
//
// It deliberately does not import the migrate package — that keeps in-package
// (package migrate) tests free to use it without an import cycle. The proof
// that it really implements migrate.Env is a compile-time assertion in the
// migrate package's own test.
package migratetest

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// ExecFunc scripts one command. It receives the container name and the joined
// command line, and answers the way the real Env does: stdout and stderr
// separately, plus the exit code (err is only for "the command could not be
// run at all").
type ExecFunc func(container, cmdline string) (stdout, stderr string, exitCode int, err error)

// FakeEnv is an in-memory migrate.Env. Every field is exported so a test can
// arrange the world directly; ExecFn scripts command output per container.
//
// Every piece of that state is guarded by mu, because a plan may touch the env
// from a worker goroutine (the dump step's file-growth watcher is one) and
// nothing but the mutex orders that against the test. The exported fields are
// for ARRANGING the world before a run; reading it back goes through the
// accessors — Log, Pulled, Reloads, PortsStripped, DirNames — or through the
// Env methods themselves (ContainerRunning, VolumeExists, ReadVolumeFile),
// which take the lock like every other caller.
type FakeEnv struct {
	mu sync.Mutex

	NS      string
	Running bool

	Containers map[string]appdef.ApplicationDef // name → def (present = running)
	Volumes    map[string]map[string]string     // volume → rel path → content
	VolSize    map[string]int64
	Files      map[string]int64 // host path → size
	Dirs       map[string]bool
	Defs       map[string]appdef.ApplicationDef // image → def returned by GenerateDefFor

	FreeHost   int64
	FreeVolume int64
	DumpRoot   string
	// SharedFS is what DumpSharesFilesystemWithVolumes answers. It defaults to
	// FALSE — two filesystems — because that is the world the fake's two
	// independent free-space knobs already model: a macOS/Windows desktop,
	// where the dump lands on the host and the new cluster inside the Docker
	// VM. A test that wants the server layout (one disk under both) sets it.
	SharedFS bool

	// ExecFn scripts command results; the zero behavior is a silent success.
	ExecFn ExecFunc
	// FailOn injects an error into a single method call, keyed "op:arg" —
	// "run:pg-src", "running:pg-src", "createvol:postgres3",
	// "pull:postgres:18", "stopns:", "reload:", "rmvol:postgres3", "rm:pg-src",
	// "mkdir:/host/x", "rmdir:/host/x", "rmdirempty:/host/x",
	// "gendef:postgres:18", "readfile:postgres2/PG_VERSION", "sharedfs:".
	FailOn map[string]error

	log           []string
	pulled        []string
	reloads       []bool
	portsStripped int
}

// New returns a FakeEnv with empty inventories and plenty of free space.
func New() *FakeEnv {
	return &FakeEnv{
		NS:         "ns1",
		Containers: map[string]appdef.ApplicationDef{},
		Volumes:    map[string]map[string]string{},
		VolSize:    map[string]int64{},
		Files:      map[string]int64{},
		Dirs:       map[string]bool{},
		Defs:       map[string]appdef.ApplicationDef{},
		FreeHost:   100 << 30,
		FreeVolume: 100 << 30,
		DumpRoot:   "/host/deps-migration",
		FailOn:     map[string]error{},
	}
}

// Record appends an entry to the call log; plans' tests assert on its order.
func (f *FakeEnv) Record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, s)
}

// Log returns a copy of the recorded mutating calls, in order.
func (f *FakeEnv) Log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// Pulled returns the images PullImage was asked for, in order.
func (f *FakeEnv) Pulled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pulled...)
}

// PortsStripped counts the RunAppDef calls whose def carried published ports
// the env had to strip.
func (f *FakeEnv) PortsStripped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.portsStripped
}

// Reloads returns the start flag of every ReloadAndStart, in order.
func (f *FakeEnv) Reloads() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.reloads...)
}

// ContainerNames returns the containers the fake currently runs, sorted.
func (f *FakeEnv) ContainerNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.Containers))
}

// ContainerDef returns the def a container was started from — what RunAppDef
// kept AFTER stripping the published ports, i.e. what the real Env would have
// handed Docker. A test that wants to know what a temp container mounts has to
// ask while it is still running, so this is called from inside an ExecFn more
// often than after a run.
func (f *FakeEnv) ContainerDef(name string) (appdef.ApplicationDef, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.Containers[name]
	return d, ok
}

// DirNames returns the directories the fake currently holds, sorted. Dirs is
// the one inventory with no Env method to read it back, so this is how a test
// inspects it without reaching past the mutex.
func (f *FakeEnv) DirNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.Dirs))
}

// failUnderLock returns the injected error for op+arg, or nil. The caller must
// already hold mu — the name says so because the map it reads is guarded by it.
func (f *FakeEnv) failUnderLock(op, arg string) error { return f.FailOn[op+":"+arg] }

// NamespaceID is the fake namespace id.
func (f *FakeEnv) NamespaceID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.NS
}

// RunAppDef records a started container, stripping the def's published ports
// first — that is the real Env's job, not the plan's, so the fake does it too
// and records that it did (a "strip-ports:<name>" log entry and the
// PortsStripped counter) instead of making the plan pre-strip them.
//
// It models the other half of the real Env's contract by omission: the def's
// InitActions and probes are NOT run. A real postgres def carries an
// init_db_and_user.sh action per datasource, and running those against the
// destination would pre-create every role and database and make the restore
// fail — so a fake that ran them would be modeling a broken Env.
func (f *FakeEnv) RunAppDef(_ context.Context, def appdef.ApplicationDef, name string, extra []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("run", name); err != nil {
		return "", err
	}
	if len(def.Ports) != 0 {
		def.Ports = nil
		f.portsStripped++
		f.log = append(f.log, "strip-ports:"+name)
	}
	f.Containers[name] = def
	f.log = append(f.log, "run:"+name+":"+def.Image+":"+strings.Join(extra, ","))
	return "id-" + name, nil
}

// ContainerRunning reports whether RunAppDef put the name in Containers.
//
// It takes an injected failure ("running:<name>") because the real Env asks
// Docker, and Docker can refuse to answer — the daemon socket goes away, the
// context expires. That is a different outcome from "not running" and the
// readiness wait has to tell them apart: one is a container that has not come
// up yet, the other is a world it can no longer see.
func (f *FakeEnv) ContainerRunning(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("running", name); err != nil {
		return false, err
	}
	_, ok := f.Containers[name]
	return ok, nil
}

// Exec delegates to ExecFn; an unknown container fails the way the real Env
// does (err set, exit code -1).
func (f *FakeEnv) Exec(_ context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	f.mu.Lock()
	_, ok := f.Containers[name]
	fn := f.ExecFn
	f.mu.Unlock()
	if !ok {
		return "", "", -1, fmt.Errorf("fake: container %s not running", name)
	}
	if fn == nil {
		return "", "", 0, nil
	}
	return fn(name, strings.Join(cmd, " "))
}

// StopRemove forgets the container; removing an unknown one succeeds.
func (f *FakeEnv) StopRemove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("rm", name); err != nil {
		return err
	}
	delete(f.Containers, name)
	f.log = append(f.log, "rm:"+name)
	return nil
}

// VolumeExists reports whether the volume is in Volumes.
func (f *FakeEnv) VolumeExists(_ context.Context, v string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Volumes[v]
	return ok, nil
}

// CreateVolume adds an empty volume.
func (f *FakeEnv) CreateVolume(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("createvol", v); err != nil {
		return err
	}
	f.Volumes[v] = map[string]string{}
	f.log = append(f.log, "createvol:"+v)
	return nil
}

// RemoveVolume forgets the volume and its size; not-found succeeds.
func (f *FakeEnv) RemoveVolume(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("rmvol", v); err != nil {
		return err
	}
	delete(f.Volumes, v)
	delete(f.VolSize, v)
	f.log = append(f.log, "rmvol:"+v)
	return nil
}

// VolumeSize answers from VolSize (0 for an unknown volume).
func (f *FakeEnv) VolumeSize(_ context.Context, v string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.VolSize[v], nil
}

// VolumeFreeBytes answers FreeVolume.
func (f *FakeEnv) VolumeFreeBytes(context.Context, string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.FreeVolume, nil
}

// DumpSharesFilesystemWithVolumes answers SharedFS.
//
// It takes an injected failure ("sharedfs:") because the real Env can fail to
// answer — a stat of a directory that is not there, an engine that will not
// talk — and "cannot tell" is not the same as "different filesystems": the
// preflight has to take the safe direction on it, and a fake that could not
// fail would leave that path untested.
func (f *FakeEnv) DumpSharesFilesystemWithVolumes(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("sharedfs", ""); err != nil {
		return false, err
	}
	return f.SharedFS, nil
}

// ReadVolumeFile answers from Volumes; a missing volume or file is an error.
func (f *FakeEnv) ReadVolumeFile(_ context.Context, v, rel string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("readfile", v+"/"+rel); err != nil {
		return "", err
	}
	files, ok := f.Volumes[v]
	if !ok {
		return "", fmt.Errorf("fake: no such volume %s", v)
	}
	c, ok := files[rel]
	if !ok {
		return "", fmt.Errorf("fake: no such file %s in %s", rel, v)
	}
	return c, nil
}

// DumpDir is DumpRoot/<id>.
func (f *FakeEnv) DumpDir(id deps.ID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return filepath.Join(f.DumpRoot, string(id))
}

// EnsureDir records the directory. The real Env creates it mode 1777 so the
// container's own uid can write the dump into it; there is no mode to model in
// memory, so the fake records the creation and TestPostgresPlanHappyPath
// asserts that "mkdir:" precedes "run:depsmig-src" in the call log.
func (f *FakeEnv) EnsureDir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("mkdir", p); err != nil {
		return err
	}
	f.Dirs[p] = true
	f.log = append(f.log, "mkdir:"+p)
	return nil
}

// RemoveDir forgets the directory.
func (f *FakeEnv) RemoveDir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("rmdir", p); err != nil {
		return err
	}
	delete(f.Dirs, p)
	f.log = append(f.log, "rmdir:"+p)
	return nil
}

// RemoveDirIfEmpty forgets the directory only when nothing else the fake
// knows about (a directory or a file) lives under it; a non-empty directory is
// kept and is not an error, exactly as os.Remove's ENOTEMPTY is ignored.
func (f *FakeEnv) RemoveDirIfEmpty(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("rmdirempty", p); err != nil {
		return err
	}
	prefix := strings.TrimSuffix(p, "/") + "/"
	for d := range f.Dirs {
		if strings.HasPrefix(d, prefix) {
			f.log = append(f.log, "rmdirempty-kept:"+p)
			return nil
		}
	}
	for file := range f.Files {
		if strings.HasPrefix(file, prefix) {
			f.log = append(f.log, "rmdirempty-kept:"+p)
			return nil
		}
	}
	delete(f.Dirs, p)
	f.log = append(f.log, "rmdirempty:"+p)
	return nil
}

// FileSize answers from Files (0 for an unknown path).
func (f *FakeEnv) FileSize(p string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Files[p], nil
}

// HostFreeBytes answers FreeHost.
func (f *FakeEnv) HostFreeBytes() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.FreeHost, nil
}

// PullImage records the image and reports 100% progress once.
func (f *FakeEnv) PullImage(_ context.Context, img string, progress func(float64)) error {
	f.mu.Lock()
	if err := f.failUnderLock("pull", img); err != nil {
		f.mu.Unlock()
		return err
	}
	f.pulled = append(f.pulled, img)
	f.log = append(f.log, "pull:"+img)
	f.mu.Unlock()
	if progress != nil {
		progress(100)
	}
	return nil
}

// IsRunning reports the fake namespace status.
func (f *FakeEnv) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Running
}

// StopNamespace clears the running flag.
func (f *FakeEnv) StopNamespace(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("stopns", ""); err != nil {
		return err
	}
	f.Running = false
	f.log = append(f.log, "stopns")
	return nil
}

// ReloadAndStart records the start flag and applies it.
func (f *FakeEnv) ReloadAndStart(_ context.Context, start bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("reload", ""); err != nil {
		return err
	}
	f.reloads = append(f.reloads, start)
	f.Running = start
	f.log = append(f.log, fmt.Sprintf("reload:%v", start))
	return nil
}

// GenerateDefFor answers from Defs, or a default postgres def carrying a
// published port, so every plan that runs a generated def exercises the
// stripping RunAppDef owes it.
func (f *FakeEnv) GenerateDefFor(_ deps.ID, image string) (appdef.ApplicationDef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("gendef", image); err != nil {
		return appdef.ApplicationDef{}, err
	}
	if d, ok := f.Defs[image]; ok {
		return d, nil
	}
	return appdef.ApplicationDef{Name: "postgres", Image: image, Ports: []string{"14523:5432"}}, nil
}
