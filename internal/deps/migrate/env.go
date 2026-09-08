package migrate

import (
	"context"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// Env is everything a migration plan may do to the world, narrowed so a plan
// can be exercised end to end against a fake. The daemon implements it over
// *docker.Client, the filesystem and the namespace Runtime (deps_env.go);
// migratetest.FakeEnv is the in-memory implementation the plan, recovery and
// integration tests share.
type Env interface {
	// NamespaceID names the namespace, for messages only.
	NamespaceID() string

	// --- containers --------------------------------------------------------
	// RunAppDef creates and starts a container from a generated app def under
	// a different name (so it never collides with the namespace's own
	// container), with published ports stripped, the launcher's temp label,
	// and extraBinds appended ("<host dir>:<container path>"). The namespace
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
	RunAppDef(ctx context.Context, def appdef.ApplicationDef, name string, extraBinds []string) (containerID string, err error)
	// ContainerRunning reports whether the named container exists and runs.
	ContainerRunning(ctx context.Context, name string) (bool, error)
	// Exec runs cmd inside the named container and returns its two output
	// streams SEPARATELY, plus the exit code. err is reserved for a failure to
	// run the command at all (no such container, the daemon refused, the
	// context expired) — a command that ran and failed reports exitCode != 0
	// with err == nil, so a caller that wants "did it work?" must look at the
	// code, never at err alone.
	//
	// The split is a contract, not a convenience: PostgreSQL's tools write
	// results to stdout and diagnostics to stderr, so an inventory query
	// (row counts, database and role lists) must be parsed from stdout ONLY —
	// a concatenated stream would make a stray notice part of the answer —
	// while a restore's errors are scanned on stderr, where psql prints them
	// even on an exit code of 0.
	Exec(ctx context.Context, name string, cmd []string) (stdout, stderr string, exitCode int, err error)
	// StopRemove stops and removes the named container; not-found is success.
	StopRemove(ctx context.Context, name string) error

	// --- data volumes (plain names as in app defs: "postgres2") ------------
	VolumeExists(ctx context.Context, volume string) (bool, error)
	CreateVolume(ctx context.Context, volume string) error
	// RemoveVolume removes the volume; not-found is success.
	RemoveVolume(ctx context.Context, volume string) error
	VolumeSize(ctx context.Context, volume string) (int64, error)
	// VolumeFreeBytes is the free space of the filesystem that holds the
	// namespace's data volumes (on macOS/Windows desktops that is the Docker
	// VM's disk, not the host's), probed through an existing volume.
	VolumeFreeBytes(ctx context.Context, probeVolume string) (int64, error)
	ReadVolumeFile(ctx context.Context, volume, rel string) (string, error)

	// --- host files --------------------------------------------------------
	// DumpDir is the host directory a migration may use for scratch files.
	DumpDir(id deps.ID) string
	// EnsureDir creates the directory (parents included) so that the CONTAINER
	// can write into it once it is bind-mounted: mode 1777, sticky and
	// world-writable, the same rule as EnsureExportDir. The daemon runs as
	// root while an image runs as its own uid (postgres is 999), so a
	// directory left with the daemon's ownership makes
	// `pg_dumpall -f /citeck/depsmig/dump.sql` die with Permission denied —
	// after the namespace has already been stopped. The sticky bit keeps one
	// dependency's migration from deleting another's dump.
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

	// --- namespace ---------------------------------------------------------
	IsRunning() bool
	// StopNamespace stops the namespace and waits until it is STOPPED.
	StopNamespace(ctx context.Context) error
	// ReloadAndStart regenerates from the current pins and, when start is
	// true, starts the namespace.
	ReloadAndStart(ctx context.Context, start bool) error
	// GenerateDefFor runs the generator with the dependency's pin forced to
	// image and returns that dependency's def — the namespace's REAL config
	// binds and Cmd for the version in question.
	GenerateDefFor(id deps.ID, image string) (appdef.ApplicationDef, error)
}
