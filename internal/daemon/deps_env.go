package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	slashpath "path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/volume"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// depsDocker is everything the dependency-migration code asks of Docker,
// narrowed to an interface so the daemon's Env can be exercised without an
// engine. *docker.Client is the only production implementation.
//
// It is the seam for BOTH users of the Docker client in this area — the
// migration Env and the seeding probe (deps_seed.go) — so a fake stands in for
// the pair rather than for half of it.
type depsDocker interface {
	ContainerName(appName string) string
	CreateNetwork(ctx context.Context) (string, error)
	CreateContainerWith(ctx context.Context, app appdef.ApplicationDef, volumesBaseDir string,
		opts docker.ContainerCreateOpts) (string, error)
	StartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string) error
	StopAndRemoveContainer(ctx context.Context, name string, timeoutSec int) error
	InspectContainer(ctx context.Context, id string) (container.InspectResponse, error)
	ExecInContainerSplit(ctx context.Context, containerID string, cmd []string) (stdout, stderr string, exitCode int, err error)
	ContainerLogs(ctx context.Context, containerID string, tail int) (string, error)
	PullImageWithProgress(ctx context.Context, img string, auth *docker.RegistryAuth, progressFn docker.PullProgressFn) error
	ImageExists(ctx context.Context, img string) bool
	EnsureUtilsImage(ctx context.Context) error
	RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error)
	RunUtilsContainerWithTimeout(ctx context.Context, cmd, binds []string, timeout time.Duration) (output string, exitCode int, err error)
	GetVolumeByOriginalName(ctx context.Context, originalName string) (*volume.Volume, error)
	CreateVolume(ctx context.Context, originalName string) (string, error)
	RemoveVolume(ctx context.Context, name string) error
	VolumeSize(ctx context.Context, name string) (int64, error)
}

// depsDockerOf boxes a possibly-nil *docker.Client into the interface without
// creating a TYPED nil: a nil pointer inside a non-nil interface value passes
// every `dc == nil` guard and then panics inside the SDK, instead of reporting
// "no docker client" the way an absent engine has always been reported.
func depsDockerOf(dc *docker.Client) depsDocker {
	if dc == nil {
		return nil
	}
	return dc
}

// Defaults for the namespace-stop wait. depsEnv carries them as FIELDS so a
// test can drive that wait without a real runtime loop; production always
// takes these values.
const (
	depsStopTimeout = 3 * time.Minute
	depsStopPoll    = 500 * time.Millisecond
)

// depsScratchDirPerm is 1777 for the same reason namespace.ExportDirPerm is:
// the daemon runs as root (or as the launcher's own uid) while the container
// that writes the dump runs as its image's user — postgres is 999 — so a
// directory carrying the daemon's ownership makes pg_dumpall die with
// Permission denied, after the namespace has already been stopped. The sticky
// bit keeps one dependency's migration from deleting another's dump.
const depsScratchDirPerm = os.ModeSticky | 0o777

// depsEnv is the production migrate.Env: Docker (the namespace-scoped client),
// the namespace's runtime files on disk, and the Runtime — all taken from ONE
// activeNamespace snapshot, so a namespace switch mid-migration cannot
// redirect a step at another namespace.
//
// LOCK ORDER: longOp → reloadMu. A migration claims d.longOp as
// longOpMigration for its WHOLE duration and every Env call runs inside it, so
// nothing here may take longOp again — ReloadAndStart takes reloadMu only.
// (doReloadEx does not take longOp itself; its HTTP callers do, before the
// call.) Nothing in this type takes configMu either: the snapshot is read once,
// at construction.
type depsEnv struct {
	d   *Daemon
	act activeNamespace
	// The seeding probe (deps_seed.go) is EMBEDDED, not held beside a second
	// copy of its parts: it is the ONE place that knows where a namespace's
	// data physically lives (a desktop's scoped named volume vs. a server's
	// bind directory), and migrate.Env asks for exactly two of its methods —
	// VolumeExists and ReadVolumeFile — with exactly its signatures. Embedding
	// satisfies both directly, so there is no second wrapping of the same
	// error ("check volume X: lookup volume X: …") and no second Docker client
	// field to keep in step with this one: e.dc IS the probe's.
	dockerDependencyProbe

	stopWait time.Duration
	stopPoll time.Duration
}

var _ migrate.Env = (*depsEnv)(nil)

// newDepsEnv builds the Env for the given active-namespace snapshot.
func (d *Daemon) newDepsEnv(act activeNamespace) *depsEnv {
	dc := depsDockerOf(act.dockerClient)
	return &depsEnv{
		d:                     d,
		act:                   act,
		dockerDependencyProbe: dockerDependencyProbe{dc: dc, volumesBase: act.volumesBase},
		stopWait:              depsStopTimeout,
		stopPoll:              depsStopPoll,
	}
}

// NamespaceID names the namespace the snapshot was taken for.
func (e *depsEnv) NamespaceID() string {
	if e.act.nsConfig != nil {
		return e.act.nsConfig.ID
	}
	return ""
}

// --- containers ------------------------------------------------------------

// RunAppDef starts one temp container from a generated def. It creates and
// starts the CONTAINER and nothing around it: no init actions, no probes, no
// runtime bookkeeping — see migrate.Env for why running the postgres def's
// init actions would break every real migration.
//
// The def is a value, and every field that is rewritten is REPLACED rather
// than mutated in place, so the caller's slices are never touched. That
// matters most for Environments: the caller's def is usually a struct copy
// sharing one backing array with the runtime's own, so appending to it in
// place would write the temp container's node identity into the namespace's
// real app def.
func (e *depsEnv) RunAppDef(ctx context.Context, def appdef.ApplicationDef, opts deps.TempContainerOpts) (string, error) {
	if e.dc == nil {
		return "", errors.New("no docker client")
	}
	// The namespace network is removed at the tail of a stop, and a migration's
	// first step stops the namespace — so by the time a temp container starts,
	// the network the def expects may well be gone.
	if _, err := e.dc.CreateNetwork(ctx); err != nil {
		return "", fmt.Errorf("ensure network: %w", err)
	}
	// Published ports would collide with the namespace's own postgres (and with
	// the other temp container); the aliases would make this container answer
	// to "postgres" on the namespace network.
	def.Ports = nil
	def.NetworkAliases = nil
	def.Volumes = append(append([]string(nil), def.Volumes...), opts.ExtraBinds...)
	def.Environments = withExtraEnv(def.Environments, opts.Env)

	name := opts.Name
	// An EMPTY name is refused rather than defaulted. docker.CreateContainerWith
	// treats "" as "no override" and builds the namespace's OWN container —
	// same name, same LabelAppName, adopted by the reconciler — which for a
	// migration means the app's container started on a temp volume, or on the
	// data the plan promised only to read. Its own guard cannot catch this: it
	// refuses an override EQUAL to the app's name, and "" is not an override at
	// all.
	if name == "" {
		return "", errors.New("a temp container needs a name of its own")
	}
	// A leftover from an interrupted run would make the create fail on a name
	// conflict. Removing it is safe: the name is the launcher's own temp name.
	_ = e.dc.StopAndRemoveContainer(ctx, e.dc.ContainerName(name), 0)

	id, err := e.dc.CreateContainerWith(ctx, def, e.act.volumesBase, docker.ContainerCreateOpts{
		Name:        name,
		ExtraLabels: map[string]string{docker.LabelTemp: docker.LabelTempValue},
		// The /etc/hosts entries that make a pinned node name resolvable. They
		// are container-local, so the temp container still answers to nothing
		// but its own name on the namespace network — the invariant a
		// Hostname override would have broken. Both halves are load-bearing
		// for RabbitMQ: without the alias the broker refuses to boot at all
		// ("epmd error for host rabbitmq: nxdomain"), and without the env it
		// boots a fresh empty node inside the copy and reports healthy.
		ExtraHosts: extraHostEntries(opts.HostAlias),
		// A temp container belongs to THIS operation and must never outlive it
		// on Docker's initiative. Without this it inherits the def's
		// unless-stopped policy, and a launcher killed mid-migration (or a host
		// reboot) would have Docker restart the target container with the NEW
		// data volume mounted — so the next start of the namespace's own
		// postgres would put a second server on that same PGDATA.
		NoRestart: true,
	})
	if err != nil {
		return "", fmt.Errorf("create %s: %w", name, err)
	}
	if err := e.dc.StartContainer(ctx, id); err != nil {
		// A container that would not start is of no use to anyone, and leaving
		// it behind would make the next attempt's name conflict the FIRST
		// symptom of this failure.
		_ = e.dc.RemoveContainer(ctx, id)
		return "", fmt.Errorf("start %s: %w", name, err)
	}
	return id, nil
}

// withExtraEnv returns base with extra applied on a COPY. Keys are applied in
// sorted order so two generations of the same temp container are byte-equal,
// and an entry already present is UPDATED in place rather than appended twice
// — an image reads the last occurrence, which would make the def's own value
// and the override disagree about what the container is running.
func withExtraEnv(base appdef.OrderedMap, extra map[string]string) appdef.OrderedMap {
	if len(extra) == 0 {
		return base
	}
	out := append(appdef.OrderedMap(nil), base...)
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		out.Set(k, extra[k])
	}
	return out
}

// extraHostEntries renders a hostname→IP map as Docker's "--add-host" strings,
// sorted so the created container is reproducible.
func extraHostEntries(alias map[string]string) []string {
	if len(alias) == 0 {
		return nil
	}
	out := make([]string, 0, len(alias))
	for _, h := range slices.Sorted(maps.Keys(alias)) {
		out = append(out, h+":"+alias[h])
	}
	return out
}

// ContainerRunning reports whether the named temp container exists and runs.
// An absent container is an ANSWER (false, nil); a failed inspect is not.
func (e *depsEnv) ContainerRunning(ctx context.Context, name string) (bool, error) {
	if e.dc == nil {
		return false, errors.New("no docker client")
	}
	info, err := e.dc.InspectContainer(ctx, e.dc.ContainerName(name))
	if err != nil {
		if isNotFoundErr(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect %s: %w", name, err)
	}
	return info.State != nil && info.State.Running, nil
}

// Exec runs cmd in the named temp container. err means the command could not
// be RUN; a command that ran and failed reports its exit code.
func (e *depsEnv) Exec(ctx context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	if e.dc == nil {
		return "", "", -1, errors.New("no docker client")
	}
	stdout, stderr, exitCode, err = e.dc.ExecInContainerSplit(ctx, e.dc.ContainerName(name), cmd)
	if err != nil {
		return stdout, stderr, exitCode, fmt.Errorf("exec in %s: %w", name, err)
	}
	return stdout, stderr, exitCode, nil
}

// depsStopContainerTimeout is the graceful-stop budget for a temp container.
// A PostgreSQL server asked to stop writes a shutdown checkpoint first, and
// killing it mid-checkpoint would leave the NEW cluster needing recovery on
// its first real start.
const depsStopContainerTimeout = 30

// StopRemove stops and removes the named temp container; not-found is success,
// which is what makes the rollback idempotent.
// ContainerLogs answers with the temp container's own output, which is the only
// place the reason a database refused to start is written down — and the
// rollback removes that container seconds later.
func (e *depsEnv) ContainerLogs(ctx context.Context, name string, tail int) (string, error) {
	if e.dc == nil {
		return "", errors.New("no docker client")
	}
	out, err := e.dc.ContainerLogs(ctx, e.dc.ContainerName(name), tail)
	if err != nil {
		return "", fmt.Errorf("logs of %s: %w", name, err)
	}
	return out, nil
}

func (e *depsEnv) StopRemove(ctx context.Context, name string) error {
	if e.dc == nil {
		return errors.New("no docker client")
	}
	if err := e.dc.StopAndRemoveContainer(ctx, e.dc.ContainerName(name), depsStopContainerTimeout); err != nil &&
		!isNotFoundErr(err) {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}

// --- volumes ---------------------------------------------------------------

// CreateVolume creates the data volume: a scoped named volume on a desktop, a
// bind directory on a server. The directory is left at 0755 and owned by the
// daemon on purpose — the official postgres entrypoint chowns PGDATA to its own
// user when it starts as root, which is how postgres2 has always worked here.
func (e *depsEnv) CreateVolume(ctx context.Context, vol string) error {
	if config.IsDesktopMode() {
		if e.dc == nil {
			return errors.New("no docker client")
		}
		if _, err := e.dc.CreateVolume(ctx, vol); err != nil {
			return fmt.Errorf("create volume %s: %w", vol, err)
		}
		return nil
	}
	//nolint:gosec // G301: Docker bind-mount sources need container-accessible perms
	if err := os.MkdirAll(e.volumeDir(vol), 0o755); err != nil {
		return fmt.Errorf("create volume %s: %w", vol, err)
	}
	return nil
}

// RemoveVolume removes the data volume; not-found is success.
func (e *depsEnv) RemoveVolume(ctx context.Context, vol string) error {
	if config.IsDesktopMode() {
		if e.dc == nil {
			return errors.New("no docker client")
		}
		v, err := e.dc.GetVolumeByOriginalName(ctx, vol)
		if err != nil {
			return fmt.Errorf("look up volume %s: %w", vol, err)
		}
		if v == nil {
			return nil
		}
		if err := e.dc.RemoveVolume(ctx, v.Name); err != nil && !isNotFoundErr(err) {
			return fmt.Errorf("remove volume %s: %w", vol, err)
		}
		return nil
	}
	if err := os.RemoveAll(e.volumeDir(vol)); err != nil {
		return fmt.Errorf("remove volume %s: %w", vol, err)
	}
	return nil
}

// Mount points and the wait budget of a volume copy.
const (
	// depsCopySrc / depsCopyDst are where the two volumes are mounted inside
	// the utils container. They are constants because two commands have to
	// agree on them — the copy and the verification that follows it.
	depsCopySrc = "/src"
	depsCopyDst = "/dst"
	// depsCopyTimeout is how long one copy may take. The default utils budget
	// is five minutes, which is right for a `cat` and absurd for a data
	// volume: a copy killed at five minutes looks exactly like a copy that
	// failed, on a namespace the migration has already stopped. The migration's
	// own context bounds it further.
	depsCopyTimeout = 4 * time.Hour
)

// copyBinds is the ONE place a volume copy says what is mounted where, and —
// the part that matters — which side is READ-ONLY.
//
// The source is the namespace's real data volume, and a copy-upgrade plan's
// whole safety argument is that nothing ever writes to it: the rollback is
// "delete the volume we made", which is only an undo while the source is
// untouched. Making that structural rather than a rule each caller remembers
// is why both the copy and its verification go through here; dstWritable is
// false for the verification, which reads both sides.
func (e *depsEnv) copyBinds(ctx context.Context, src, dst string, dstWritable bool) ([]string, error) {
	srcRef, err := e.volumeMountSource(ctx, src)
	if err != nil {
		return nil, err
	}
	dstRef, err := e.volumeMountSource(ctx, dst)
	if err != nil {
		return nil, err
	}
	dstBind := dstRef + ":" + depsCopyDst
	if !dstWritable {
		dstBind += ":ro"
	}
	return []string{srcRef + ":" + depsCopySrc + ":ro", dstBind}, nil
}

// volumeMountSource turns a plain data-volume name into what Docker has to be
// given to mount it: the scoped NAMED volume on a desktop, the bind directory
// on a server. A volume that is not there is an error rather than an
// auto-created empty directory — an empty source would produce a faithful copy
// of nothing, which for RabbitMQ boots as a brand-new node and reports healthy.
func (e *depsEnv) volumeMountSource(ctx context.Context, vol string) (string, error) {
	if !config.IsDesktopMode() {
		dir := e.volumeDir(vol)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("volume %s: %w", vol, err)
		}
		return dir, nil
	}
	if e.dc == nil {
		return "", errors.New("no docker client")
	}
	v, err := e.dc.GetVolumeByOriginalName(ctx, vol)
	if err != nil {
		return "", fmt.Errorf("look up volume %s: %w", vol, err)
	}
	if v == nil {
		return "", fmt.Errorf("volume %s not found", vol)
	}
	return v.Name, nil
}

// CopyVolume copies the CONTENTS of src into dst through the utils container,
// preserving ownership, mode and mtimes, and then VERIFIES the result.
//
// -S is what keeps a sparse file sparse. ZooKeeper preallocates its txnlog to
// 64 MiB and writes it as a hole; without -S tar reads the hole back as zeros
// and the copy lands 64 MiB per txnlog on the destination (measured: 16 KiB of
// blocks became 65556 KiB). With it the copy is 16 KiB again, and on data with
// no holes the two are byte-identical, so it costs nothing where it does
// nothing. The utils image ships GNU tar 1.35, which has it.
//
// `tar -cf - | tar -xpf -` is the mechanism because ownership is not cosmetic
// here: the data is owned by the image's uid (rabbitmq 999, zookeeper 1000,
// postgres 999), and a copy that lands root-owned is not a slower migration,
// it is a broker that will not start — a .erlang.cookie RabbitMQ cannot read
// fails its boot with "eacces" → "Kernel pid terminated" (measured). The utils
// container runs as root, which is what lets tar restore an owner the daemon
// may not even be allowed to name. `cp -a` was not chosen: it needs the same
// privileges and reports a partial copy less clearly than a tar that exits.
//
// The verification is a second utils run comparing the file count and the
// total size of the two mounts. It is what turns a partial copy — a pipe that
// broke, a device that filled up mid-stream — into a failed step instead of a
// container started on half a data directory.
func (e *depsEnv) CopyVolume(ctx context.Context, src, dst string) error {
	if e.dc == nil {
		return errors.New("no docker client")
	}
	if err := e.dc.EnsureUtilsImage(ctx); err != nil {
		return fmt.Errorf("ensure utils image: %w", err)
	}
	binds, err := e.copyBinds(ctx, src, dst, true)
	if err != nil {
		return err
	}
	out, code, err := e.dc.RunUtilsContainerWithTimeout(ctx,
		[]string{"sh", "-c", "tar -C " + depsCopySrc + " -Scf - . | tar -C " + depsCopyDst + " -Sxpf -"},
		binds, depsCopyTimeout)
	if err != nil {
		return fmt.Errorf("copy volume %s to %s: %w", src, dst, err)
	}
	if code != 0 {
		return fmt.Errorf("copy volume %s to %s: exit %d: %s", src, dst, code, strings.TrimSpace(out))
	}
	return e.verifyVolumeCopy(ctx, src, dst)
}

// verifyVolumeCopy compares the two mounts after a copy: the number of files
// and the number of BYTES IN THEM. Both are read from ONE utils container, in
// labeled lines, so a stray diagnostic on the container's stderr cannot be
// parsed as part of the answer.
//
// Apparent size, and emphatically NOT block allocation. This compared `du -sk`
// once and failed EVERY real RabbitMQ copy-upgrade at the copy step
// ("36 files / 252 KiB against 36 files / 256 KiB") on a copy whose content was
// byte-identical: mnesia preallocates past EOF, so schema.DAT was 22093 bytes
// in 56*512 blocks on the source and the same 22093 bytes in 48*512 on the
// copy. A faithful copy is entitled to allocate differently — filesystems round
// to blocks, honor holes and drop preallocation as they see fit — so
// allocation is not a statement about the data at all.
//
// What the check exists for is a copy that lost DATA: a pipe that broke, a
// device that filled mid-stream. Both leave fewer files or fewer bytes, which
// is exactly what these two numbers see.
func (e *depsEnv) verifyVolumeCopy(ctx context.Context, src, dst string) error {
	binds, err := e.copyBinds(ctx, src, dst, false)
	if err != nil {
		return err
	}
	out, code, err := e.dc.RunUtilsContainerWithTimeout(ctx,
		[]string{"sh", "-c", volumeMeasureScript}, binds, depsCopyTimeout)
	if err != nil {
		return fmt.Errorf("verify the copy of %s: %w", src, err)
	}
	if code != 0 {
		return fmt.Errorf("verify the copy of %s: exit %d: %s", src, code, strings.TrimSpace(out))
	}
	m, err := parseVolumeMeasure(out)
	if err != nil {
		return fmt.Errorf("verify the copy of %s: %w", src, err)
	}
	if m["srcfiles"] != m["dstfiles"] || m["srcbytes"] != m["dstbytes"] {
		return fmt.Errorf(
			"the copy of %s into %s does not match the source: %d files / %d bytes against %d files / %d bytes",
			src, dst, m["dstfiles"], m["dstbytes"], m["srcfiles"], m["srcbytes"])
	}
	return nil
}

// volumeMeasureScript prints four LABELED numbers, one per line. Labels rather
// than bare numbers because RunUtilsContainer returns the container's whole
// log: an unexpected line on stderr would otherwise be read as one of the
// measurements.
//
// `du --apparent-size` would be the obvious spelling of the byte totals and is
// not available: the utils image's du is BUSYBOX and rejects the option
// (verified by running it in the image). `stat -c %s` is present there, and
// `find -exec ... {} +` batches it into one exec per directory rather than one
// per file.
var volumeMeasureScript = `echo "srcfiles $(` + volumeCountFiles(depsCopySrc) + `)"; ` +
	`echo "srcbytes $(` + volumeSumBytes(depsCopySrc) + `)"; ` +
	`echo "dstfiles $(` + volumeCountFiles(depsCopyDst) + `)"; ` +
	`echo "dstbytes $(` + volumeSumBytes(depsCopyDst) + `)"`

// volumeCountFiles / volumeSumBytes are the two shell fragments the measurement
// is built from, in one place, so the copy verification and the size probe
// cannot drift into measuring different things.
func volumeCountFiles(mount string) string { return "find " + mount + " -type f | wc -l" }

// volumeSumBytes sums the APPARENT size of every regular file. `+0` in the END
// block makes an empty tree print 0 rather than an empty line, so "no files" is
// still a number the parser accepts.
func volumeSumBytes(mount string) string {
	return "find " + mount + ` -type f -exec stat -c %s {} + | awk '{s+=$1} END {print s+0}'`
}

// parseVolumeMeasure reads the four labeled numbers volumeMeasureScript prints.
func parseVolumeMeasure(out string) (map[string]int64, error) {
	return parseLabeledNumbers(out, "srcfiles", "srcbytes", "dstfiles", "dstbytes")
}

// parseLabeledNumbers reads "<label> <number>" lines out of a utils container's
// log, ignoring everything else in it — a container's whole output comes back,
// so a warning on stderr must not be readable as a measurement.
//
// EVERY requested label must be present. A measurement that did not happen must
// not be served as a zero: zero is a real answer here (an empty volume, a
// matching pair of counts) and would sail through the very check it exists to
// fail.
func parseLabeledNumbers(out string, want ...string) (map[string]int64, error) {
	got := map[string]int64{}
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		got[f[0]] = n
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			return nil, fmt.Errorf("no %s in the measurement output: %q", k, strings.TrimSpace(out))
		}
	}
	return got, nil
}

// EnsureVolumeDirs creates directories inside a data volume, through the utils
// container so they end up owned by ROOT — which is what the real init
// container that normally creates them produces too (ZooKeeper's entrypoint
// chowns its data directories before it drops privileges).
//
// It exists because a temp container runs the CONTAINER and nothing around it:
// no init actions, no probes, and no INIT CONTAINERS. ZooKeeper's generated
// def has an init container whose only job is `mkdir -p /zkdir/data
// /zkdir/datalog`, so a copy of a volume that has never held a running
// ZooKeeper would start the temp container against directories that are not
// there.
func (e *depsEnv) EnsureVolumeDirs(ctx context.Context, vol string, dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	if e.dc == nil {
		return errors.New("no docker client")
	}
	args := make([]string, 0, len(dirs)+2)
	args = append(args, "mkdir", "-p")
	for _, dir := range dirs {
		clean, err := volumeRelPath(dir)
		if err != nil {
			return fmt.Errorf("ensure %s in %s: %w", dir, vol, err)
		}
		args = append(args, depsCopyDst+"/"+clean)
	}
	if err := e.dc.EnsureUtilsImage(ctx); err != nil {
		return fmt.Errorf("ensure utils image: %w", err)
	}
	ref, err := e.volumeMountSource(ctx, vol)
	if err != nil {
		return err
	}
	out, code, err := e.dc.RunUtilsContainer(ctx, args, []string{ref + ":" + depsCopyDst})
	if err != nil {
		return fmt.Errorf("create directories in %s: %w", vol, err)
	}
	if code != 0 {
		return fmt.Errorf("create directories in %s: exit %d: %s", vol, code, strings.TrimSpace(out))
	}
	return nil
}

// volumeRelPath validates a path a caller wants created INSIDE a volume. The
// callers are the launcher's own CopySpecs, so this is not a trust boundary —
// it is a guard against a typo escaping the mount ("../../etc") and having the
// utils container, which runs as root, create it somewhere else entirely.
func volumeRelPath(rel string) (string, error) {
	slashed := strings.ReplaceAll(rel, `\`, "/")
	// The ".." check runs on the RAW path, before Clean. Clean("/../../etc")
	// is "/etc": it would silently turn an escape into a plausible-looking
	// path instead of refusing it, which for a root container is the worst of
	// both answers.
	for part := range strings.SplitSeq(slashed, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q leaves the volume", rel)
		}
	}
	clean := strings.TrimPrefix(slashpath.Clean("/"+slashed), "/")
	if clean == "" || clean == "." {
		return "", errors.New("empty path")
	}
	return clean, nil
}

// DependencyState is the namespace's current pin for id — the image its data
// runs on and which GENERATION of the data volume that is. An unpinned
// dependency (and a namespace with no runtime) answers the zero value, whose
// Gen() is 1: the volume every namespace that has never migrated runs.
func (e *depsEnv) DependencyState(id deps.ID) deps.DependencyState {
	if e.act.runtime == nil {
		return deps.DependencyState{}
	}
	return e.act.runtime.DependencyStates()[id]
}

// VolumeSize is what one generation of a dependency's data COSTS: the LARGER of
// its apparent size and the space it actually occupies. An absent volume
// measures 0 rather than failing — the preflight asks about the TARGET volume
// too, which normally does not exist yet.
//
// The larger of two readings, and not either one, because neither is an upper
// bound and both directions were MEASURED on real volumes:
//
//   - allocation UNDER-reads sparse data. ZooKeeper preallocates its txnlog to
//     64 MiB: log.1 is 67108880 apparent bytes in 16 KiB of blocks, and a plain
//     tar copy of it lands as 65556 KiB on the destination. Requiring the 16 KiB
//     is an ENOSPC in the middle of a migration, on an already-stopped
//     namespace, 4096x under.
//   - apparent size UNDER-reads dense data, because filesystems round to blocks
//     and honor preallocation past EOF: a 22093-byte mnesia schema.DAT occupies
//     28672 bytes.
//
// The copy does preserve holes (tar -S, see CopyVolume), which usually makes
// the destination cost the source's allocation rather than its apparent size.
// That is an efficiency and NOT a reason to require less: whether the holes
// survive is a property of the DESTINATION filesystem, which this launcher
// cannot see. Over-requiring refuses a migration that would have fit and says
// exactly what it wanted; under-requiring is the ENOSPC. That is the same
// direction DumpSharesFilesystemWithVolumes already chose.
//
// It is also what makes the two MODES agree. Server mode summed apparent size
// while desktop ran `du -sk` and got allocation, so the same ZooKeeper volume
// was worth 64 MiB to one preflight and 16 KiB to the other.
func (e *depsEnv) VolumeSize(ctx context.Context, vol string) (int64, error) {
	if config.IsDesktopMode() {
		return e.desktopVolumeSize(ctx, vol)
	}
	var apparent, allocated int64
	err := filepath.WalkDir(e.volumeDir(vol), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, ierr := d.Info(); ierr == nil {
			apparent += info.Size()
			allocated += fileAllocatedBytes(info)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure volume %s: %w", vol, err)
	}
	return max(apparent, allocated), nil
}

// desktopVolumeSize measures a scoped named volume from INSIDE the engine: on a
// macOS/Windows desktop the host cannot see into it at all, and even on Linux
// the volume root is not readable by the desktop user.
//
// Both numbers come from ONE container run, and the volume is mounted READ-ONLY
// — measuring must never be able to change what it measures.
func (e *depsEnv) desktopVolumeSize(ctx context.Context, vol string) (int64, error) {
	if e.dc == nil {
		return 0, errors.New("no docker client")
	}
	v, err := e.dc.GetVolumeByOriginalName(ctx, vol)
	if err != nil {
		return 0, fmt.Errorf("look up volume %s: %w", vol, err)
	}
	if v == nil {
		return 0, nil
	}
	if imgErr := e.dc.EnsureUtilsImage(ctx); imgErr != nil {
		return 0, fmt.Errorf("ensure utils image: %w", imgErr)
	}
	out, code, err := e.dc.RunUtilsContainer(ctx,
		[]string{"sh", "-c", volumeSizeScript}, []string{v.Name + ":" + volumeSizeMount + ":ro"})
	if err != nil {
		return 0, fmt.Errorf("measure volume %s: %w", vol, err)
	}
	if code != 0 {
		return 0, fmt.Errorf("measure volume %s: exit %d: %s", vol, code, strings.TrimSpace(out))
	}
	m, err := parseLabeledNumbers(out, "bytes", "kb")
	if err != nil {
		return 0, fmt.Errorf("measure volume %s: %w", vol, err)
	}
	return max(m["bytes"], m["kb"]*1024), nil
}

// volumeSizeMount is where the measured volume is mounted inside the utils
// container.
const volumeSizeMount = "/vol"

// volumeSizeScript prints a volume's two sizes, LABELED for the same reason the
// copy verification labels its own: RunUtilsContainer returns the container's
// whole log, so a stray line on stderr must not be readable as a measurement.
//
// `du -sk` is the right tool HERE and the wrong one in the copy verification,
// and the difference is the whole point: this is a question about DISK, where
// blocks are the answer, while that one is a question about DATA, where they
// are not.
var volumeSizeScript = `echo "bytes $(` + volumeSumBytes(volumeSizeMount) + `)"; ` +
	`echo "kb $(du -sk ` + volumeSizeMount + ` | cut -f1)"`

// VolumeFreeBytes is the free space of the filesystem that holds the data
// volumes. On a server that is a host directory; on a macOS/Windows desktop the
// named volumes live inside the Docker VM, whose disk the host cannot see at
// all — so the engine is asked, through a utils container mounting a volume
// that already exists.
func (e *depsEnv) VolumeFreeBytes(ctx context.Context, probeVolume string) (int64, error) {
	if !config.IsDesktopMode() {
		return freeBytesAt(e.serverVolumesDir())
	}
	if e.dc == nil {
		return 0, errors.New("no docker client")
	}
	v, err := e.dc.GetVolumeByOriginalName(ctx, probeVolume)
	if err != nil {
		return 0, fmt.Errorf("look up volume %s: %w", probeVolume, err)
	}
	if v == nil {
		return 0, fmt.Errorf("volume %s not found", probeVolume)
	}
	if imgErr := e.dc.EnsureUtilsImage(ctx); imgErr != nil {
		return 0, fmt.Errorf("ensure utils image: %w", imgErr)
	}
	out, code, err := e.dc.RunUtilsContainer(ctx, []string{"df", "-kP", "/vol"}, []string{v.Name + ":/vol:ro"})
	if err != nil {
		return 0, fmt.Errorf("df on volume %s: %w", probeVolume, err)
	}
	if code != 0 {
		return 0, fmt.Errorf("df on volume %s: exit %d: %s", probeVolume, code, strings.TrimSpace(out))
	}
	kb, err := parseDfAvailableKB(out)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

// serverVolumesDir is the directory holding every bind-mounted data volume.
// It falls back to volumesBase when it does not exist yet — the two are on the
// same filesystem, which is all this measurement is about.
func (e *depsEnv) serverVolumesDir() string {
	dir := filepath.Join(e.act.volumesBase, "volumes")
	if _, err := os.Stat(dir); err != nil {
		return e.act.volumesBase
	}
	return dir
}

// DumpSharesFilesystemWithVolumes answers whether the scratch directory the
// dump is written to and the data volumes are on ONE filesystem — which is
// what decides whether the preflight has to require room for both at once.
//
// SERVER mode is the case that motivated the seam, and the one that can be
// answered exactly: both are directories under the namespace's volumes base
// (the dump under <base>/deps-migration/<id>, the volumes under
// <base>/volumes), so the filesystem identity of those two paths IS the
// answer. It is not always "yes" — an operator is free to mount the volumes
// directory onto its own disk — which is why it is measured rather than
// assumed. The comparison uses the volumes BASE rather than the dump
// directory, which does not exist until the migration creates it; that is the
// same path HostFreeBytes measures, for the same reason.
//
// DESKTOP mode cannot be answered from here, so it is answered deliberately,
// per OS — see desktopSharesHostFilesystem.
func (e *depsEnv) DumpSharesFilesystemWithVolumes(_ context.Context, _ string) (bool, error) {
	if config.IsDesktopMode() {
		return desktopSharesHostFilesystem(goruntime.GOOS), nil
	}
	same, err := fsutil.SameFilesystem(e.act.volumesBase, e.serverVolumesDir())
	if err != nil {
		return false, fmt.Errorf("compare the dump and volume filesystems: %w", err)
	}
	return same, nil
}

// desktopSharesHostFilesystem is the desktop rule, as a pure function of the
// OS so the choice itself is testable on any of them.
//
// On macOS and Windows the named volumes live inside the Docker VM, whose disk
// the host cannot see at all — that is why VolumeFreeBytes has to ask the
// engine through a container there. Two filesystems, certainly.
//
// On Linux the engine runs on the same kernel, and on an ordinary desktop
// install its volume root sits on the same disk as the user's data directory.
// We cannot PROVE it: /var/lib/docker is unreadable to the desktop user, so
// stat'ing the volume's mountpoint fails, and a separate /var partition, a
// rootless engine and a remote DOCKER_HOST all look identical from here. So
// this takes the safe direction and says yes. Over-requiring refuses a
// migration that would have fit and says exactly what it wanted;
// under-requiring runs the restore out of space on a stopped namespace.
func desktopSharesHostFilesystem(goos string) bool { return goos == "linux" }

// parseDfAvailableKB reads the "Available" column of `df -kP` (POSIX format:
// Filesystem, 1024-blocks, Used, Available, Capacity, Mounted on). It scans
// from the LAST line up, because df wraps a long device name onto its own line
// and puts the numbers on the next one — a wrapped row is 5 fields with the
// device missing, an ordinary one is 6. Neither shape can pick up the header,
// whose fourth (or third) column is the word "Available".
func parseDfAvailableKB(out string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		f := strings.Fields(lines[i])
		if len(f) < 5 {
			continue
		}
		// On a wrapped row the device name is missing, so the Available column
		// is the 3rd field rather than the 4th.
		idx := 3
		if len(f) == 5 {
			idx = 2
		}
		if kb, err := strconv.ParseInt(f[idx], 10, 64); err == nil {
			return kb, nil
		}
	}
	return 0, fmt.Errorf("no data row in df output: %q", out)
}

// --- host files ------------------------------------------------------------

// depsMigrationDir is the scratch parent every dependency's migration shares.
func depsMigrationDir(volumesBase string) string { return filepath.Join(volumesBase, "deps-migration") }

// DumpDir is one dependency's scratch directory, under the namespace's own
// runtime directory (so it is on the same filesystem the operator sized for
// this namespace, and a namespace delete takes it with it).
func (e *depsEnv) DumpDir(id deps.ID) string {
	return filepath.Join(depsMigrationDir(e.act.volumesBase), string(id))
}

// EnsureDir creates the scratch directory 1777 — sticky and world-writable —
// so the CONTAINER's own uid can write into it once it is bind-mounted. The
// chmod is not redundant: MkdirAll applies the umask, and an existing
// directory keeps whatever mode it already had. Same rule as EnsureExportDir.
func (e *depsEnv) EnsureDir(path string) error {
	if err := os.MkdirAll(path, depsScratchDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := os.Chmod(path, depsScratchDirPerm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// RemoveDir removes the directory and everything under it.
func (e *depsEnv) RemoveDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// RemoveDirIfEmpty removes the directory only if it holds nothing. A directory
// that is not empty is left alone and is NOT an error — the scratch parent is
// shared, so a migration tidying up after itself must not delete another
// dependency's dump. An absent directory is already gone.
func (e *depsEnv) RemoveDirIfEmpty(path string) error {
	err := os.Remove(path)
	// fs.ErrExist is the PORTABLE spelling of "not empty" — the standard
	// library maps both ENOTEMPTY and Windows' ERROR_DIR_NOT_EMPTY onto it
	// (syscall.Errno.Is) — so this needs no per-platform errno and no
	// "read the directory back and see" second guess.
	if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrExist) {
		return nil
	}
	return fmt.Errorf("remove %s: %w", path, err)
}

// FileSize reports the size of a host file; a file that is not there is 0, not
// a failure — the dump does not exist until pg_dumpall creates it, and the
// progress reporter starts watching before that.
func (e *depsEnv) FileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return st.Size(), nil
}

// HostFreeBytes is the free space of the filesystem the dump is written to.
func (e *depsEnv) HostFreeBytes() (int64, error) {
	return freeBytesAt(e.act.volumesBase)
}

// freeBytesAt measures a filesystem through an existing path. The stat is what
// separates "the disk is full" from "that path is not there": AvailableDiskSpace
// answers 0 for both, and a preflight would report a full disk for a namespace
// whose runtime directory is simply missing.
func freeBytesAt(path string) (int64, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return fsutil.AvailableDiskSpace(path), nil
}

// --- images ----------------------------------------------------------------

// PullImage pulls the target image with the workspace's registry credentials,
// reporting percent progress.
func (e *depsEnv) PullImage(ctx context.Context, image string, progress func(float64)) error {
	if e.dc == nil {
		return errors.New("no docker client")
	}
	var auth *docker.RegistryAuth
	if e.d != nil && e.d.store != nil {
		bindings, _ := e.d.store.ListRegistryBindings(e.act.workspaceID)
		if fn := makeRegistryAuthFunc(e.act.workspaceConfig, e.d.secretReaderFunc(), bindings); fn != nil {
			auth = fn(image)
		}
	}
	err := e.dc.PullImageWithProgress(ctx, image, auth, func(_, _ float64, pct int) {
		if progress != nil {
			progress(float64(pct))
		}
	})
	if err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// ImageExists reports whether the image is already in the LOCAL image store.
//
// It answers a plain bool and swallows nothing it could have reported: the
// underlying docker.Client.ImageExists is itself an inspect-or-false, so "not
// here" and "I could not ask" are already one answer by the time it returns —
// and a daemon with no Docker client at all is the same answer again. A false
// therefore means "not known to be here", which is the honest input to a
// WARNING and never to a refusal. That is the whole contract: the rollback
// preflight uses it to tell the operator up front that the image it is about
// to need may have to be pulled, instead of letting that land after the
// namespace has already been stopped.
func (e *depsEnv) ImageExists(ctx context.Context, image string) bool {
	if e.dc == nil {
		return false
	}
	return e.dc.ImageExists(ctx, image)
}

// --- namespace -------------------------------------------------------------

// IsRunning reports whether the namespace is anything but STOPPED — STARTING
// and FAILED count, because containers exist in both.
func (e *depsEnv) IsRunning() bool {
	return e.act.runtime != nil && e.act.runtime.Status() != namespace.NsStatusStopped
}

// StopNamespace stops the namespace and waits until it reports STOPPED. A
// namespace that will not stop FAILS the step: the plan has created nothing at
// that point, so there is nothing to roll back, while carrying on would dump a
// cluster that is still being written to.
//
// What it CANNOT tell apart is a slow stop from a stop that was never going to
// happen: Runtime.Stop only ENQUEUES cmdStop, and a runtime whose loop is not
// running drains nothing — so a namespace that is not stopping at all costs the
// full timeout before failing, with the same error. That precondition — start a
// migration only when the runtime loop is alive or the namespace is already
// STOPPED — belongs to the route that starts one (Task 12), not here: this
// method has no way to ask whether a command it enqueued will ever be read. The
// same trap is documented for the Update & Start queue's STOPPING arm.
func (e *depsEnv) StopNamespace(ctx context.Context) error {
	rt := e.act.runtime
	if rt == nil || rt.Status() == namespace.NsStatusStopped {
		return nil
	}
	rt.Stop()
	deadline := time.Now().Add(e.stopWait)
	for {
		if rt.Status() == namespace.NsStatusStopped {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("namespace %s did not stop within %s (status %s)",
				e.NamespaceID(), e.stopWait, rt.Status())
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for namespace %s to stop: %w", e.NamespaceID(), ctx.Err())
		case <-time.After(e.stopPoll):
		}
	}
}

// ReloadAndStart regenerates the namespace from the pins as they now stand
// (the commit has already moved the migrated one) and, when start is true,
// starts it again.
//
// It takes reloadMu and ONLY reloadMu: the migration holds d.longOp as
// longOpMigration for its whole run and this call happens inside it, so
// claiming the long-operation lock again would deadlock the finalize step. See
// the type's lock-order note.
func (e *depsEnv) ReloadAndStart(_ context.Context, start bool) error {
	// The reload acts on whatever namespace is ACTIVE, so an Env built for a
	// different one must refuse rather than reload a stranger. Two callers make
	// that reachable: crash recovery, whose Env describes a namespace that is
	// not installed yet (it clears WasRunning precisely so this stays
	// unreachable), and a namespace switch racing a migration. Refusing is the
	// only safe answer — a migration's finalize reports it as a warning, and
	// the data has already moved by then.
	if e.d == nil {
		return errors.New("reload: no daemon")
	}
	if active := namespaceIDOf(e.d.active()); active != e.NamespaceID() {
		return fmt.Errorf("reload of namespace %q refused: %q is active", e.NamespaceID(), active)
	}
	e.d.reloadMu.Lock()
	defer e.d.reloadMu.Unlock()
	if start {
		// The namespace is STOPPED at this point, so the freshly resolved set
		// is applied by STARTING it rather than by regenerating a runtime that
		// is not running.
		if err := e.d.invokeReloadEx(false, true, false); err != nil {
			return fmt.Errorf("reload and start: %w", err)
		}
		return nil
	}
	// A namespace that was stopped stays stopped, but still needs the reload:
	// its generated files and its app catalog must follow the new layout.
	if err := e.d.invokeReload(); err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	return nil
}

// GenerateDefFor runs the namespace's REAL generation with one dependency's
// pin forced to st, and returns that dependency's def — the same Cmd, the same
// config binds and the same layout the namespace's own container would get for
// that version, mounting the volume of the generation st names.
//
// The returned def is GUARANTEED to carry the requested image AND the
// requested volume — see the checks at the tail. It writes NOTHING: no runtime files, no runtime state. The config files the
// returned def binds are already on disk from the last real reload, and they
// do not depend on the dependency's version (postgres' postgresql.conf,
// pg_hba.conf and init_db_and_user.sh are the same files for every major —
// only the image, PGDATA and the volume move). The runtime's pins, generated
// defs and config are the reload path's to move, not this function's.
func (e *depsEnv) GenerateDefFor(id deps.ID, st deps.DependencyState) (appdef.ApplicationDef, error) {
	d, ok := deps.Lookup(id)
	if !ok {
		return appdef.ApplicationDef{}, fmt.Errorf("unknown dependency %q", id)
	}
	return e.GenerateDefForVolume(id, st, deps.VolumeName(d, st.Gen()))
}

// GenerateDefForVolume is GenerateDefFor's more general form: it runs the same
// generation and demands the same guarantee on the image, but mounts VOLUME
// instead of whatever deps.VolumeName(d, st.Gen()) would ordinarily pick.
//
// It exists for exactly one caller: a multi-rung PostgreSQL walk's
// intermediate clusters, which live in a SCRATCH volume that has no
// generation of its own — deps.VolumeName cannot produce "postgres3-hop", see
// deps.ScratchVolumeName. The generator itself has no notion of a scratch
// volume, so the substitution happens HERE, after generation, by rewriting the
// mount the generator picked for st's own generation: this is the one place a
// container of a migration plan learns which volume it mounts, and
// GenerateDefFor is a one-line wrapper over it rather than the reverse.
func (e *depsEnv) GenerateDefForVolume(id deps.ID, st deps.DependencyState, mountVolume string) (appdef.ApplicationDef, error) {
	d, ok := deps.Lookup(id)
	if !ok {
		return appdef.ApplicationDef{}, fmt.Errorf("unknown dependency %q", id)
	}
	rt := e.act.runtime
	if rt == nil || e.act.nsConfig == nil || e.act.bundleDef == nil {
		return appdef.ApplicationDef{}, errors.New("no namespace loaded")
	}
	// DependencyStates returns a copy, so overriding one entry cannot reach the
	// runtime's own map.
	pins := rt.DependencyStates()
	pins[id] = st

	genOpts := namespace.GenerateOpts{
		DetachedApps:     rt.ManualStoppedApps(),
		EditedFileEdits:  rt.FileEditsSnapshot(),
		EditedAppPatches: rt.AppPatchesSnapshot(),
		ExtraLicenses:    collectExtraLicensesFrom(e.d.licenses),
		DependencyStates: pins,
	}
	if e.d.secretService != nil {
		genOpts.SecretReader = e.d.nsSecretReader()
	}
	genOpts.DiskContent = readDiskContent(e.act.volumesBase, genOpts.EditedFileEdits)

	resp, err := namespace.Generate(e.act.nsConfig, e.act.bundleDef, e.act.workspaceConfig,
		e.act.systemSecrets, genOpts)
	if err != nil {
		return appdef.ApplicationDef{}, fmt.Errorf("generate namespace %q: %w", e.act.nsConfig.ID, err)
	}
	for _, a := range resp.Applications {
		if a.Name != d.AppName() {
			continue
		}
		// The forced pin must survive the whole generation. It does today only
		// because the plan asks for a BREAKING version, which the pin gate
		// therefore emits verbatim — a non-breaking `to` would be resolved to
		// the bundle's candidate instead, and the caller would silently get a
		// temp container running a different image from the one it named. That
		// is a container started on somebody's data under a false name, so it
		// is an error, not a surprise to debug later.
		if a.Image != st.Image {
			return appdef.ApplicationDef{}, fmt.Errorf(
				"the generator resolved %s to %q, not to the requested %q", d.AppName(), a.Image, st.Image)
		}
		// The generator mounted the volume ST'S OWN generation names — it has
		// no notion of a scratch volume — so when the caller asked for a
		// DIFFERENT one, retarget the mount rather than trust the generator to
		// have produced it. ordinary is what the pin gate guarantees is there;
		// substituting only that exact source is what keeps this from ever
		// touching a bind that merely happens to end in the same word.
		ordinary := deps.VolumeName(d, st.Gen())
		if mountVolume != "" && ordinary != "" && mountVolume != ordinary {
			if !mountsVolume(a, ordinary) {
				return appdef.ApplicationDef{}, fmt.Errorf(
					"the generator gave %s the volumes %v, not the expected %q", d.AppName(), a.Volumes, ordinary)
			}
			a.Volumes = retargetVolume(a.Volumes, ordinary, mountVolume)
			// retargetVolume's own correctness is not enough to rest the
			// "source is only ever read" invariant on: it substitutes every
			// entry whose source matches ordinary today, but nothing upstream
			// of this line would notice if a future change to it (or to
			// whatever the generator emits) left a SECOND bind — a WAL-archive
			// mount, a PGDATA subdirectory, anything a bundle adds — still
			// pointing at ordinary. So the postcondition is proven directly,
			// not assumed: after retargeting, the def must not mount ordinary
			// at all. A def that still does is refused rather than handed to a
			// migration plan that promises the source is only ever read.
			if mountsVolume(a, ordinary) {
				return appdef.ApplicationDef{}, fmt.Errorf(
					"the generator still gave %s a bind to %q after retargeting it to %q: %v",
					d.AppName(), ordinary, mountVolume, a.Volumes)
			}
		}
		// The same guard for the other half of the pin, and it is the one a
		// copy-upgrade plan (and a multi-rung postgres walk) rests on: every
		// container a migration plan starts must land on the volume it asked
		// for. A def that mounts the SOURCE volume instead would run the old
		// image, the new image and the whole restore against the namespace's
		// real data — the one thing a migration plan promises never to touch —
		// and nothing downstream would notice.
		if mountVolume != "" && !mountsVolume(a, mountVolume) {
			return appdef.ApplicationDef{}, fmt.Errorf(
				"the generator gave %s the volumes %v, not the requested %q", d.AppName(), a.Volumes, mountVolume)
		}
		return a, nil
	}
	return appdef.ApplicationDef{}, fmt.Errorf("the generator produced no %s app", d.AppName())
}

// retargetVolume rewrites the SOURCE half of the one volume entry that
// mounts "from" to "to", leaving every other entry untouched. Only the source
// is compared — see mountsVolume — so a bind of a host file that happens to
// end in the same word is never touched.
func retargetVolume(vols []string, from, to string) []string {
	out := make([]string, len(vols))
	for i, v := range vols {
		src, rest, ok := strings.Cut(v, ":")
		if ok && src == from {
			out[i] = to + ":" + rest
			continue
		}
		out[i] = v
	}
	return out
}

// mountsVolume reports whether the def mounts the named data volume. A def's
// volume entry is "<source>:<container path>[:opts]", and only the SOURCE is
// compared: a bind of a host file that happens to end in the same word is not
// this dependency's data.
//
// It scans def.Volumes ONLY — never def.InitContainers[].Volumes — which is
// narrower than "does the def mount X" and is worth stating rather than
// leaving to be assumed: the two callers above use this to prove a migration
// temp container cannot touch the source, and that proof is sound only
// because RunAppDef (the one caller that starts a container from a def this
// function checked) runs a single container from the def and never runs its
// init containers at all — see runTemp/RunAppDef. If a future caller ever ran
// init containers from one of these defs, an init-container-only bind would
// be invisible here.
func mountsVolume(def appdef.ApplicationDef, name string) bool {
	for _, v := range def.Volumes {
		if src, _, ok := strings.Cut(v, ":"); ok && src == name {
			return true
		}
	}
	return false
}
