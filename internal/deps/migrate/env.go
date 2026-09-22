package migrate

import (
	"context"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// Env is everything a migration plan may do to the world, narrowed so a plan
// can be exercised end to end against a fake. The daemon implements it over
// *docker.Client, the filesystem and the namespace Runtime (deps_env.go);
// migratetest.FakeEnv is the in-memory implementation the plan tests and the
// daemon's crash-recovery tests share. The integration test does NOT use it —
// it runs the daemon's real depsEnv against real containers, which is the
// point of it.
type Env interface {
	// NamespaceID names the namespace, for messages only.
	NamespaceID() string

	// DependencyState is the namespace's CURRENT pin for id: the image its
	// data runs on and, load-bearing here, which GENERATION of the data volume
	// that is. A migration is a move from that generation to the next one, so
	// both the volume it reads and the volume it creates are derived from this
	// — which is why it lives on the Env rather than being a plan argument:
	// Preflight has no options struct, and measuring the source volume is the
	// first thing it does.
	//
	// An unpinned dependency answers the zero value, whose Gen() is 1 — the
	// generation every namespace that has never migrated runs.
	DependencyState(id deps.ID) deps.DependencyState

	// --- containers --------------------------------------------------------
	// RunAppDef creates and starts a container from a generated app def under
	// opts.Name (so it never collides with the namespace's own container),
	// with published ports stripped, the launcher's temp label, and
	// opts.ExtraBinds appended ("<host dir>:<container path>"). The namespace
	// network is created if missing.
	//
	// It runs the CONTAINER and nothing around it: the def's InitActions must
	// NOT be executed, and neither must its startup/liveness probes. This is a
	// hard precondition of the PostgreSQL plan, not a simplification. The
	// generated postgres def carries one `/init_db_and_user.sh <db>` init
	// action per webapp datasource plus one for Keycloak
	// (generator_webapp.go, generator_keycloak.go), so running them against
	// the destination would pre-create every role and database before the
	// dump is replayed — and the restore would then fail on 100% of real
	// migrations with `role "citeck_emodel" already exists`, an error the
	// parser deliberately does not tolerate. The dump already carries every
	// role and database; the temp container only has to serve it.
	RunAppDef(ctx context.Context, def appdef.ApplicationDef, opts TempContainerOpts) (containerID string, err error)
	// ContainerRunning reports whether the named container exists and runs.
	ContainerRunning(ctx context.Context, name string) (bool, error)
	// Exec runs cmd inside a container of this namespace — one of the temp
	// containers, or an app of the namespace by its app name — and returns its
	// two output
	// streams SEPARATELY, plus the exit code. err is reserved for a failure to
	// run the command at all (no such container, the daemon refused, the
	// context expired) — a command that ran and failed reports exitCode != 0
	// with err == nil, so a caller that wants "did it work?" must look at the
	// code, never at err alone.
	//
	// The split is a contract, not a convenience: PostgreSQL's tools write
	// results to stdout and diagnostics to stderr, so an inventory query
	// (the database and role lists, the per-database user-table count) must be
	// parsed from stdout ONLY — a concatenated stream would make a stray
	// notice part of the answer —
	// while a restore's errors are scanned on stderr, where psql prints them
	// even on an exit code of 0.
	Exec(ctx context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error)
	// ContainerLogs returns the last tail lines a container printed. It is the
	// only way to say WHY a temp container never became ready: the rollback
	// removes it moments later, and after that the reason is gone for good.
	// A container that does not exist, or a daemon that will not answer, is
	// reported as an error — the caller degrades to "no logs", never to a
	// failure of the migration itself.
	ContainerLogs(ctx context.Context, name string, tail int) (string, error)
	// StopRemove stops and removes the named container; not-found is success.
	StopRemove(ctx context.Context, name string) error

	// --- data volumes (plain names as in app defs: "postgres2") ------------
	VolumeExists(ctx context.Context, volume string) (bool, error)
	CreateVolume(ctx context.Context, volume string) error
	// RemoveVolume removes the volume; not-found is success.
	RemoveVolume(ctx context.Context, volume string) error
	// CopyVolume copies the CONTENTS of src into dst, preserving ownership,
	// mode and mtimes. src is mounted READ-ONLY: it is the namespace's real
	// data volume, and this is the only place in a copy-upgrade plan that
	// names it at all.
	//
	// Ownership is not cosmetic. The data is owned by the image's uid
	// (rabbitmq 999, zookeeper 1000, postgres 999), and a copy that lands
	// root-owned is not a slower migration, it is a broker that will not
	// start: a .erlang.cookie RabbitMQ cannot read fails its boot with
	// "eacces" → "Kernel pid terminated" (measured), and PostgreSQL refuses a
	// PGDATA that is not 0700 and its own.
	//
	// It is expected to take minutes to hours on a real volume, so an
	// implementation must not bound it by the timeout it uses for a `cat`.
	CopyVolume(ctx context.Context, src, dst string) error
	// EnsureVolumeDirs creates directories inside a data volume, relative to
	// its root, parents included, owned by root — which is what the real init
	// container that normally creates them produces too (the ZooKeeper
	// entrypoint chowns its data directories before it drops privileges).
	//
	// It exists because a temp container runs the CONTAINER and nothing around
	// it: no init actions, no probes and no INIT CONTAINERS. ZooKeeper's
	// generated def has an init container whose only job is
	// `mkdir -p /zkdir/data /zkdir/datalog`, so a copy of a volume that has
	// never held a running ZooKeeper would have the temp container start
	// against directories that are not there.
	EnsureVolumeDirs(ctx context.Context, volume string, dirs []string) error
	VolumeSize(ctx context.Context, volume string) (int64, error)
	// VolumeFreeBytes is the free space of the filesystem that holds the
	// namespace's data volumes (on macOS/Windows desktops that is the Docker
	// VM's disk, not the host's), probed through an existing volume.
	VolumeFreeBytes(ctx context.Context, probeVolume string) (int64, error)
	ReadVolumeFile(ctx context.Context, volume, rel string) (string, error)
	// DumpSharesFilesystemWithVolumes reports whether the scratch directory the
	// dump is written to (DumpDir) and the data volumes are on ONE filesystem.
	//
	// Only the environment can answer it: the plan knows a host path and a
	// volume NAME, and nothing about a name says where its bytes land. On a
	// server both are directories under the namespace's volumes base (usually
	// one filesystem, but an operator is free to mount the volumes directory
	// onto its own disk); on a macOS/Windows desktop the volumes live inside
	// the Docker VM, whose disk the host cannot see at all.
	//
	// It matters because the dump and the new cluster COEXIST — the scratch
	// directory is removed only after the commit, and the new cluster is built
	// next to the old data — so on one filesystem what must fit is the SUM of
	// the two, which is what the preflight demands when this answers true.
	// probeVolume is one of the namespace's existing volumes, for
	// implementations that have to ask the engine.
	DumpSharesFilesystemWithVolumes(ctx context.Context, probeVolume string) (bool, error)

	// --- host files --------------------------------------------------------
	// DumpDir is the host directory a migration may use for scratch files.
	DumpDir(id deps.ID) string
	// EnsureDir creates the directory (parents included) so that the CONTAINER
	// can write into it once it is bind-mounted: mode 1777, sticky and
	// world-writable, the same rule as EnsureExportDir. The daemon runs as
	// root while an image runs as its own uid (postgres is 999), so a
	// directory left with the daemon's ownership makes the dump step's
	// `gzip -1 > /citeck/depsmig/dump.sql.gz` redirect die with Permission
	// denied — after the namespace has already been stopped.
	//
	// World-writable is the part that does the work; the sticky bit is what
	// makes world-writable safe, by restricting unlinking inside the directory
	// to the owner of each file. It says nothing about the SHARED PARENT
	// ("<volumes>/deps-migration"), which every dependency's scratch directory
	// sits under: what keeps one migration from removing another's dump there
	// is that each gets its own subdirectory (DumpDir) and that the parent is
	// only ever removed when it is empty (RemoveDirIfEmpty).
	EnsureDir(path string) error
	RemoveDir(path string) error
	// RemoveDirIfEmpty removes the directory only if it holds nothing. A
	// directory that is not empty is left alone and is NOT an error: the
	// scratch parent ("<volumes>/deps-migration") is shared, so a migration
	// tidying up after itself must not delete another dependency's dump.
	RemoveDirIfEmpty(path string) error
	FileSize(path string) (int64, error)
	HostFreeBytes() (int64, error)

	// --- images ------------------------------------------------------------
	PullImage(ctx context.Context, image string, progress func(percent float64)) error
	// ImageExists reports whether the image is already in the LOCAL image
	// store. It answers a plain bool and no error on purpose: the underlying
	// capability (docker.Client.ImageExists) cannot distinguish "not here" from
	// "I could not ask", and neither can the only caller act on the difference.
	// A false therefore means "not known to be here", which is the honest input
	// to a WARNING and never to a refusal.
	//
	// It exists for the rollback preflight (ruling on OPEN QUESTION 6): the
	// previous image really ran, so the pin names a real registry — but the
	// host may have pruned it and the registry may be unreachable, and without
	// this the failure would land after the namespace is already stopped.
	ImageExists(ctx context.Context, image string) bool

	// --- namespace ---------------------------------------------------------
	IsRunning() bool
	// StopNamespace stops the namespace and waits until it is STOPPED.
	StopNamespace(ctx context.Context) error
	// ReloadAndStart regenerates from the current pins and, when start is
	// true, starts the namespace.
	ReloadAndStart(ctx context.Context, start bool) error
	// GenerateDefFor runs the generator with the dependency's pin forced to st
	// and returns that dependency's def — the namespace's REAL config binds
	// and Cmd for the version in question, mounting the volume of the
	// generation st names.
	//
	// Both halves of st are load-bearing and an implementation must verify
	// both: a temp container started on somebody else's data under a false
	// name is not a surprise to debug later. The image guard has always been
	// there; the volume one is what a copy-upgrade plan rests on, since every
	// container it runs must land on the COPY and never on the source.
	GenerateDefFor(id deps.ID, st deps.DependencyState) (appdef.ApplicationDef, error)
	// GenerateDefForVolume is GenerateDefFor's more general form: the same
	// generation, the same guarantee that the image is right, but the volume
	// mounted is VOLUME rather than whatever deps.VolumeName(d, st.Gen())
	// would ordinarily pick.
	//
	// It exists for exactly one caller: a multi-rung PostgreSQL walk's
	// intermediate clusters, which live in a SCRATCH volume that cannot be
	// expressed as a generation at all — deps.VolumeName has no generation
	// that produces "postgres3-hop" (see deps.ScratchVolumeName). Nothing else
	// may build a def from scratch or edit a returned def's Volumes list
	// directly: GenerateDefFor stays a one-line wrapper over this rather than
	// the other way around, so a container still learns its volume in exactly
	// ONE place.
	GenerateDefForVolume(id deps.ID, st deps.DependencyState, volume string) (appdef.ApplicationDef, error)
}

// TempContainerOpts is everything about a temp container that is not in the
// generated def. See deps.TempContainerOpts for what each field is for and
// why the type is declared over there rather than here.
type TempContainerOpts = deps.TempContainerOpts
