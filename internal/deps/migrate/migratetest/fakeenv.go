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
	"strconv"
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
	Logs       map[string]string                // container → what ContainerLogs answers
	VolSize    map[string]int64
	Files      map[string]int64 // host path → size
	Dirs       map[string]bool
	// Defs is keyed by DefKey(image, gen) — the pair GenerateDefFor is asked
	// for. A def registered for one generation is NOT returned for another:
	// the whole point of the key is that "which volume does this container
	// mount" is answerable in a test.
	Defs map[string]appdef.ApplicationDef
	// States is the namespace's pin record per dependency, what
	// Env.DependencyState answers. An id that is absent answers the zero
	// value, whose Gen() is 1 — a namespace that has never migrated.
	States map[deps.ID]deps.DependencyState
	// LocalImages is the local image store ImageExists answers from. An image
	// that is absent is "not known to be here" — the same reading the real Env
	// gives it — which is what makes the rollback preflight's pull warning
	// testable in both directions.
	LocalImages map[string]bool

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
	// "gendef:postgres:18@1", "readfile:postgres2/PG_VERSION", "sharedfs:",
	// "copy:postgres2", "voldirs:rabbitmq3", "volexists:postgres2".
	FailOn map[string]error

	log           []string
	pulled        []string
	reloads       []bool
	portsStripped int
	runOpts       map[string]RunRecord
	volDirs       map[string][]string
	// removedVolumes is every volume RemoveVolume was asked to remove, in
	// order — including a volume that was never there, since a rollback's
	// removal calls are idempotent and a test asserting on WHAT was asked for
	// must see them all.
	removedVolumes []string
	// createdVolumes is the set of volumes CreateVolume made that RemoveVolume
	// has not yet undone — bookkeeping over those two calls, not a mirror of
	// Volumes: a volume a test seeds directly (the namespace's own source
	// data) was never "created" by the migration and must not appear in
	// LiveVolumes.
	createdVolumes map[string]bool
	// dumps is the set of host paths a dump write (an Exec'd pg_dumpall this
	// fake recognizes) has put down that a later removal has not yet undone.
	// Bookkeeping over Exec and RemoveDir, the two calls a dump's lifecycle
	// already goes through — nothing here is new production behavior.
	dumps map[string]bool
	// trace is every dump and cluster-volume creation/removal this fake
	// observed, in order — what TestPostgresLadderDeletesEachDumpBeforeTakingTheNext
	// walks to prove the peak never exceeds one dump plus one cluster.
	trace []TraceEvent
	// execCmds is every argv Exec was asked to run, in order — the raw slice,
	// not the joined string ExecFn receives. Bookkeeping over a call the fake
	// already receives: it exists so a test can inspect the SHAPE of a command
	// (e.g. that a dump ran as `bash -c "…"` and not a bare argv) without
	// reconstructing it from the call log's joined strings.
	execCmds [][]string
}

// TraceEvent is one creation or removal of a dump file or a data volume, in
// the order the fake observed it.
type TraceEvent struct {
	// Kind is "dump" or "cluster".
	Kind string
	Name string
	// Created is true when the thing came into existence, false when it was
	// removed.
	Created bool
}

// RunRecord is what RunAppDef was asked for, beyond the def: the temp
// container's extra environment and its /etc/hosts aliases. A test asserts on
// it because those two are the RabbitMQ node-identity pin, and both halves are
// mandatory — the env without the alias is a broker that will not boot.
type RunRecord struct {
	Env       map[string]string
	HostAlias map[string]string
	Binds     []string
}

// DefKey is the key GenerateDefFor answers on: an image AND the generation of
// the volume the def must mount. It is exported because a test arranging Defs
// has to spell the same key the fake looks up.
func DefKey(image string, gen int) string { return image + "@" + strconv.Itoa(gen) }

// New returns a FakeEnv with empty inventories and plenty of free space.
func New() *FakeEnv {
	return &FakeEnv{
		NS:             "ns1",
		Containers:     map[string]appdef.ApplicationDef{},
		Logs:           map[string]string{},
		Volumes:        map[string]map[string]string{},
		VolSize:        map[string]int64{},
		Files:          map[string]int64{},
		Dirs:           map[string]bool{},
		Defs:           map[string]appdef.ApplicationDef{},
		States:         map[deps.ID]deps.DependencyState{},
		LocalImages:    map[string]bool{},
		runOpts:        map[string]RunRecord{},
		volDirs:        map[string][]string{},
		createdVolumes: map[string]bool{},
		dumps:          map[string]bool{},
		FreeHost:       100 << 30,
		FreeVolume:     100 << 30,
		DumpRoot:       "/host/deps-migration",
		FailOn:         map[string]error{},
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

// RemovedVolumes returns every volume RemoveVolume was asked to remove, in
// order.
func (f *FakeEnv) RemovedVolumes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedVolumes...)
}

// LiveVolumes returns the volumes CreateVolume made that RemoveVolume has not
// yet undone, sorted. A volume a test seeded directly (the namespace's own
// source data) was never CREATED by the plan and is deliberately not in it.
func (f *FakeEnv) LiveVolumes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.createdVolumes))
}

// LiveDumps returns the host paths of every dump write this fake has observed
// (via Exec) that no removal (via RemoveDir) has undone yet, sorted.
func (f *FakeEnv) LiveDumps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.dumps))
}

// DumpsWritten returns the host path of every dump write this fake has ever
// observed, in order — including one that was later removed. Unlike
// LiveDumps, which answers "still on disk right now", this is what a test
// asserting every RUNG's dump was compressed needs: an intermediate rung's
// dump is gone (restored and removed) by the time a multi-hop migration
// finishes, and by then LiveDumps would report nothing at all.
func (f *FakeEnv) DumpsWritten() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.trace {
		if ev.Kind == "dump" && ev.Created {
			out = append(out, ev.Name)
		}
	}
	return out
}

// ExecutedCommands returns every argv Exec was asked to run, in order — the
// raw command slice, not the joined string ExecFn receives. A test that needs
// to inspect the SHAPE of a command (e.g. that the dump step ran `bash -c
// "…gzip…"` rather than a bare `pg_dumpall` argv) reads this instead of
// reconstructing it from the joined call log.
func (f *FakeEnv) ExecutedCommands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.execCmds))
	for i, c := range f.execCmds {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// Trace returns a copy of every dump and cluster-volume creation/removal this
// fake has observed, in order.
func (f *FakeEnv) Trace() []TraceEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TraceEvent(nil), f.trace...)
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
func (f *FakeEnv) RunAppDef(_ context.Context, def appdef.ApplicationDef, opts deps.TempContainerOpts) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := opts.Name
	if err := f.failUnderLock("run", name); err != nil {
		return "", err
	}
	if len(def.Ports) != 0 {
		def.Ports = nil
		f.portsStripped++
		f.log = append(f.log, "strip-ports:"+name)
	}
	f.Containers[name] = def
	f.runOpts[name] = RunRecord{
		Env:       maps.Clone(opts.Env),
		HostAlias: maps.Clone(opts.HostAlias),
		Binds:     slices.Clone(opts.ExtraBinds),
	}
	f.log = append(f.log, "run:"+name+":"+def.Image+":"+strings.Join(opts.ExtraBinds, ","))
	return "id-" + name, nil
}

// RunOpts returns what RunAppDef was asked for under a container name. It
// survives StopRemove: a test asserting on the node identity a temp container
// carried usually only gets to look once the plan has finished with it.
func (f *FakeEnv) RunOpts(name string) (RunRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runOpts[name]
	return r, ok
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
//
// It also recognizes a successful pg_dumpall and records the dump it wrote —
// bookkeeping over a call the fake already receives, not new production
// behavior: the real dump step has no other way to make a fake "write" a
// host file, since Exec is the only call that carries the in-container path
// pg_dumpall was told to use, resolved to a host path through the very binds
// RunAppDef recorded for this container.
func (f *FakeEnv) Exec(_ context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	f.mu.Lock()
	f.execCmds = append(f.execCmds, append([]string(nil), cmd...))
	_, ok := f.Containers[name]
	fn := f.ExecFn
	binds := append([]string(nil), f.runOpts[name].Binds...)
	f.mu.Unlock()
	if !ok {
		return "", "", -1, fmt.Errorf("fake: container %s not running", name)
	}
	if fn == nil {
		stdout, stderr, exitCode, err = "", "", 0, nil
	} else {
		stdout, stderr, exitCode, err = fn(name, strings.Join(cmd, " "))
	}
	if err == nil && exitCode == 0 {
		if hostPath, isDump := dumpWritePath(cmd, binds); isDump {
			f.mu.Lock()
			f.Files[hostPath] = dumpPlaceholderSize
			f.dumps[hostPath] = true
			f.trace = append(f.trace, TraceEvent{Kind: "dump", Name: hostPath, Created: true})
			f.log = append(f.log, "dump-write:"+hostPath)
			f.mu.Unlock()
		}
	}
	return stdout, stderr, exitCode, err
}

// dumpPlaceholderSize is the size the fake records for an auto-tracked dump
// write. Tests that care about a real size (the progress-watcher ones) set
// Files directly and never go through this path.
const dumpPlaceholderSize = 1 << 10

// dumpWritePath recognizes a DumpScript-shaped command — `bash -c "set -o
// pipefail; pg_dumpall … | gzip -N > '<path>'"` — and resolves the
// in-container redirect target to a host path through binds — the extra
// binds RunAppDef recorded for the container this command ran in. ok=false
// for every other command, or one with no matching bind.
//
// The command is no longer a bare `pg_dumpall …` argv (that shape predates
// the dump being piped into gzip through bash -c — see DumpScript in
// postgres_restore.go), so recognition can no longer key on cmd[0] or scan for
// a "-f" flag: it has to read the redirect target out of the script string.
func dumpWritePath(cmd, binds []string) (hostPath string, ok bool) {
	if len(cmd) != 3 || cmd[0] != "bash" || cmd[1] != "-c" {
		return "", false
	}
	script := cmd[2]
	if !strings.Contains(script, "pg_dumpall") {
		return "", false
	}
	inContainer, ok := dumpRedirectTarget(script)
	if !ok {
		return "", false
	}
	for _, b := range binds {
		host, ctr, cut := strings.Cut(b, ":")
		if !cut {
			continue
		}
		if inContainer == ctr {
			return host, true
		}
		if rel, isChild := strings.CutPrefix(inContainer, ctr+"/"); isChild {
			return host + "/" + rel, true
		}
	}
	return "", false
}

// dumpRedirectTarget extracts the single-quoted path following "> " at the
// end of a DumpScript-shaped command line — the in-container path gzip's
// stdout was redirected to.
func dumpRedirectTarget(script string) (string, bool) {
	idx := strings.LastIndex(script, "> '")
	if idx == -1 {
		return "", false
	}
	rest := script[idx+len("> '"):]
	if !strings.HasSuffix(rest, "'") {
		return "", false
	}
	return strings.TrimSuffix(rest, "'"), true
}

// ContainerLogs answers from Logs, keyed by container name. An unknown
// container is an error, exactly as a removed one is for the real Env.
func (f *FakeEnv) ContainerLogs(_ context.Context, name string, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("logs", name); err != nil {
		return "", err
	}
	out, ok := f.Logs[name]
	if !ok {
		return "", fmt.Errorf("no such container: %s", name)
	}
	return out, nil
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
	// The injected failure is not a nicety: the real Env can fail to answer
	// (a daemon that will not talk), and "cannot check" is a different verdict
	// from "it is not there" — one is a Docker problem, the other is data the
	// operator may have deleted themselves.
	if err := f.failUnderLock("volexists", v); err != nil {
		return false, err
	}
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
	f.createdVolumes[v] = true
	f.trace = append(f.trace, TraceEvent{Kind: "cluster", Name: v, Created: true})
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
	f.removedVolumes = append(f.removedVolumes, v)
	if f.createdVolumes[v] {
		delete(f.createdVolumes, v)
		f.trace = append(f.trace, TraceEvent{Kind: "cluster", Name: v, Created: false})
	}
	f.log = append(f.log, "rmvol:"+v)
	return nil
}

// CopyVolume deep-copies the source volume's file map into dst, creating dst
// if it is not there, and records "copy:<src>-><dst>".
//
// It models the real Env's contract by construction rather than by comment:
// the source map is only ever READ here, so a plan that leaned on the copy
// being an alias would see its writes vanish instead of quietly corrupting the
// namespace's own data in a test that then passed.
func (f *FakeEnv) CopyVolume(_ context.Context, src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("copy", src); err != nil {
		return err
	}
	from, ok := f.Volumes[src]
	if !ok {
		return fmt.Errorf("fake: no such volume %s", src)
	}
	f.Volumes[dst] = maps.Clone(from)
	f.VolSize[dst] = f.VolSize[src]
	f.log = append(f.log, "copy:"+src+"->"+dst)
	return nil
}

// EnsureVolumeDirs records the directories asked for, per volume. There is no
// mode or ownership to model in memory, so what a test can assert is that the
// plan asked for them, on the COPY, before it started anything on it.
func (f *FakeEnv) EnsureVolumeDirs(_ context.Context, volume string, dirs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("voldirs", volume); err != nil {
		return err
	}
	if _, ok := f.Volumes[volume]; !ok {
		return fmt.Errorf("fake: no such volume %s", volume)
	}
	f.volDirs[volume] = append(f.volDirs[volume], dirs...)
	f.log = append(f.log, "voldirs:"+volume+":"+strings.Join(dirs, ","))
	return nil
}

// VolumeDirs returns the directories EnsureVolumeDirs was asked to create in a
// volume, in order.
func (f *FakeEnv) VolumeDirs(volume string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.volDirs[volume]...)
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
// RemoveDir removes whatever is at p — a directory entry, a tracked dump
// file, or a directory's contents recursively — mirroring the real Env, whose
// RemoveDir is backed by os.RemoveAll and does not distinguish a scratch
// directory from a single dump file inside it. This is what makes Finalize's
// whole-directory cleanup also clear a dump this fake is still tracking as
// live, exactly as it would on a real filesystem.
func (f *FakeEnv) RemoveDir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUnderLock("rmdir", p); err != nil {
		return err
	}
	f.removeRecursivelyUnderLock(p)
	f.log = append(f.log, "rmdir:"+p)
	return nil
}

// removeRecursivelyUnderLock deletes p itself (a Dirs entry, a Files entry, or
// a tracked dump) plus anything nested under it as a directory, clearing the
// trace as any tracked dump goes. The caller must already hold mu.
func (f *FakeEnv) removeRecursivelyUnderLock(p string) {
	delete(f.Dirs, p)
	f.forgetDumpUnderLock(p)
	prefix := strings.TrimSuffix(p, "/") + "/"
	for d := range f.Dirs {
		if strings.HasPrefix(d, prefix) {
			delete(f.Dirs, d)
		}
	}
	for file := range maps.Clone(f.Files) {
		if file == p || strings.HasPrefix(file, prefix) {
			f.forgetDumpUnderLock(file)
			delete(f.Files, file)
		}
	}
}

// forgetDumpUnderLock clears a tracked dump at p, if there is one, and
// records its removal on the trace. The caller must already hold mu.
func (f *FakeEnv) forgetDumpUnderLock(p string) {
	if !f.dumps[p] {
		return
	}
	delete(f.dumps, p)
	f.trace = append(f.trace, TraceEvent{Kind: "dump", Name: p, Created: false})
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

// ImageExists answers from LocalImages. An image nobody arranged is not there,
// which is the same reading the real Env gives an image it cannot find.
func (f *FakeEnv) ImageExists(_ context.Context, img string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.LocalImages[img]
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

// DependencyState answers States; an unpinned dependency is the zero value,
// whose Gen() is 1.
func (f *FakeEnv) DependencyState(id deps.ID) deps.DependencyState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.States[id]
}

// GenerateDefFor answers from Defs, keyed by image AND generation, or builds a
// default def that mounts the generation's volume — so a test that never
// arranges Defs still gets a def whose volume line says which generation it
// was generated for, which is what "the temp container mounts the copy" is
// asserted on.
func (f *FakeEnv) GenerateDefFor(id deps.ID, st deps.DependencyState) (appdef.ApplicationDef, error) {
	vol := "vol-" + string(id)
	if d, ok := deps.Lookup(id); ok {
		if v := deps.VolumeName(d, st.Gen()); v != "" {
			vol = v
		}
	}
	return f.GenerateDefForVolume(id, st, vol)
}

// GenerateDefForVolume answers from Defs keyed by image AND generation — the
// same key GenerateDefFor uses, since a scratch-volume def differs from the
// ordinary one ONLY in which volume it mounts, and an arranged Defs entry is
// returned as-is, exactly as GenerateDefFor already promised — or builds a
// default def that mounts VOLUME, whatever it is. That is what "this
// container mounts the scratch volume" is asserted on in a test that never
// arranges Defs.
func (f *FakeEnv) GenerateDefForVolume(id deps.ID, st deps.DependencyState, volume string) (appdef.ApplicationDef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := DefKey(st.Image, st.Gen())
	if err := f.failUnderLock("gendef", key); err != nil {
		return appdef.ApplicationDef{}, err
	}
	if d, ok := f.Defs[key]; ok {
		return d, nil
	}
	name := string(id)
	if d, ok := deps.Lookup(id); ok {
		name = d.AppName()
	}
	if volume == "" {
		volume = "vol-" + string(id)
	}
	return appdef.ApplicationDef{
		Name:    name,
		Image:   st.Image,
		Ports:   []string{"14523:5432"},
		Volumes: []string{volume + ":/data"},
	}, nil
}
