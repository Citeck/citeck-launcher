package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	PullImageWithProgress(ctx context.Context, img string, auth *docker.RegistryAuth, progressFn docker.PullProgressFn) error
	EnsureUtilsImage(ctx context.Context) error
	RunUtilsContainer(ctx context.Context, cmd, binds []string) (output string, exitCode int, err error)
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
	dc  depsDocker
	// probe is the seeding probe (deps_seed.go) — the ONE place that knows
	// where a namespace's data physically lives (a desktop's scoped named
	// volume vs. a server's bind directory). Volume reads and the server-mode
	// volume path are delegated to it rather than re-derived here.
	probe dockerDependencyProbe

	stopWait time.Duration
	stopPoll time.Duration
}

var _ migrate.Env = (*depsEnv)(nil)

// newDepsEnv builds the Env for the given active-namespace snapshot.
func (d *Daemon) newDepsEnv(act activeNamespace) *depsEnv {
	dc := depsDockerOf(act.dockerClient)
	return &depsEnv{
		d:        d,
		act:      act,
		dc:       dc,
		probe:    dockerDependencyProbe{dc: dc, volumesBase: act.volumesBase},
		stopWait: depsStopTimeout,
		stopPoll: depsStopPoll,
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
// The def is a value, and the three fields that are rewritten are replaced
// rather than mutated in place, so the caller's slices are never touched.
func (e *depsEnv) RunAppDef(ctx context.Context, def appdef.ApplicationDef, name string, extraBinds []string) (string, error) {
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
	def.Volumes = append(append([]string(nil), def.Volumes...), extraBinds...)

	// A leftover from an interrupted run would make the create fail on a name
	// conflict. Removing it is safe: the name is the launcher's own temp name.
	_ = e.dc.StopAndRemoveContainer(ctx, e.dc.ContainerName(name), 0)

	id, err := e.dc.CreateContainerWith(ctx, def, e.act.volumesBase, docker.ContainerCreateOpts{
		Name:        name,
		ExtraLabels: map[string]string{docker.LabelTemp: docker.LabelTempValue},
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

// VolumeExists reports whether the plain-named data volume exists, through the
// same rule seeding uses.
func (e *depsEnv) VolumeExists(ctx context.Context, vol string) (bool, error) {
	exists, err := e.probe.VolumeExists(ctx, vol)
	if err != nil {
		return false, fmt.Errorf("check volume %s: %w", vol, err)
	}
	return exists, nil
}

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
	if err := os.MkdirAll(e.probe.volumeDir(vol), 0o755); err != nil {
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
	if err := os.RemoveAll(e.probe.volumeDir(vol)); err != nil {
		return fmt.Errorf("remove volume %s: %w", vol, err)
	}
	return nil
}

// VolumeSize measures the volume's data. An absent volume measures 0 rather
// than failing: the preflight asks about the TARGET volume too, which normally
// does not exist yet.
func (e *depsEnv) VolumeSize(ctx context.Context, vol string) (int64, error) {
	if config.IsDesktopMode() {
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
		size, err := e.dc.VolumeSize(ctx, v.Name)
		if err != nil {
			return 0, fmt.Errorf("measure volume %s: %w", vol, err)
		}
		return size, nil
	}
	var total int64
	err := filepath.WalkDir(e.probe.volumeDir(vol), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure volume %s: %w", vol, err)
	}
	return total, nil
}

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

// ReadVolumeFile reads a file out of a data volume — the seeding probe's rule,
// not a second copy of it.
func (e *depsEnv) ReadVolumeFile(ctx context.Context, vol, rel string) (string, error) {
	out, err := e.probe.ReadVolumeFile(ctx, vol, rel)
	if err != nil {
		return "", err
	}
	return out, nil
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
// pin forced to image, and returns that dependency's def — the same Cmd, the
// same config binds and the same layout the namespace's own container would
// get for that version.
//
// It writes NOTHING: no runtime files, no runtime state. The config files the
// returned def binds are already on disk from the last real reload, and they
// do not depend on the dependency's version (postgres' postgresql.conf,
// pg_hba.conf and init_db_and_user.sh are the same files for every major —
// only the image, PGDATA and the volume move). The runtime's pins, generated
// defs and config are the reload path's to move, not this function's.
func (e *depsEnv) GenerateDefFor(id deps.ID, image string) (appdef.ApplicationDef, error) {
	d, ok := deps.Lookup(id)
	if !ok {
		return appdef.ApplicationDef{}, fmt.Errorf("unknown dependency %q", id)
	}
	rt := e.act.runtime
	if rt == nil || e.act.nsConfig == nil || e.act.bundleDef == nil {
		return appdef.ApplicationDef{}, errors.New("no namespace loaded")
	}
	// DependencyPins returns a copy, so overriding one entry cannot reach the
	// runtime's own map.
	pins := rt.DependencyPins()
	pins[id] = image

	genOpts := namespace.GenerateOpts{
		DetachedApps:     rt.ManualStoppedApps(),
		EditedFileEdits:  rt.FileEditsSnapshot(),
		EditedAppPatches: rt.AppPatchesSnapshot(),
		ExtraLicenses:    collectExtraLicensesFrom(e.d.licenses),
		DependencyPins:   pins,
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
		if a.Name == d.AppName() {
			return a, nil
		}
	}
	return appdef.ApplicationDef{}, fmt.Errorf("the generator produced no %s app", d.AppName())
}
