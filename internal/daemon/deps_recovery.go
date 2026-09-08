package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
)

// recoverInterruptedMigration is run for a namespace that has been LOADED but
// not started yet. A migration journal surviving into a fresh process means
// the daemon (or the desktop app) died mid-migration, and what the journal
// describes — the half-built target volume, the two temp containers — is still
// on the host. One of those temp containers has the namespace's OWN data
// volume mounted, so starting the namespace on top of it would put a second
// PostgreSQL server on one PGDATA. The rollback therefore has to happen
// before the runtime does anything, which is why this is synchronous.
//
// It returns whether the namespace should be started because the interrupted
// migration had stopped it (the journal's WasRunning) — and ONLY when the
// rollback actually succeeded: RollbackInterrupted's rolledBack means
// ATTEMPTED, and a failed rollback leaves the journal open with leftovers that
// must not be handed a running namespace. That failure is not swallowed: the
// verdict is recorded, the journal stays for the next start to retry, and the
// dependency list reports it (rollbackPendingMessage).
//
// With no journal it only sweeps a stale scratch directory: a crash AFTER the
// commit but before the tidy leaves a dump nobody will ever read. While a
// journal is open that directory is exactly what the pending rollback still
// describes, so the sweep is skipped there.
func (d *Daemon) recoverInterruptedMigration(ctx context.Context, act activeNamespace) (restart bool) {
	rt := act.runtime
	if rt == nil {
		return false
	}
	env := d.depsEnvFor(act)
	j := rt.MigrationJournal()
	if j == nil {
		if err := env.RemoveDir(depsMigrationDir(act.volumesBase)); err != nil {
			slog.Warn("Failed to remove a stale dependency-migration scratch dir", "err", err)
		}
		return false
	}
	rollback := func(ctx context.Context, j *deps.MigrationJournal) error {
		switch j.ID {
		case deps.Postgres:
			// WasRunning is cleared for the rollback itself: restarting the
			// namespace is the CALLER's decision here, because at this point
			// the namespace is not installed as the daemon's active one and
			// ReloadAndStart would act on whichever namespace is.
			jj := *j
			jj.WasRunning = false
			return migrate.RollbackPostgres(ctx, env, &jj)
		default:
			// Never clear a journal we cannot undo: it is the only record of
			// what was left behind, and the id is what tells the operator
			// which launcher version could finish the job.
			return fmt.Errorf("this launcher has no rollback for an interrupted migration of %q", j.ID)
		}
	}
	if _, err := migrate.RollbackInterrupted(ctx, rt, rollback); err != nil {
		//nolint:gosec // G706: the id comes from the persisted journal, whose values this launcher wrote
		slog.Error("Rollback of an interrupted dependency migration failed",
			"dependency", j.ID, "ns", namespaceIDOf(act), "err", err)
		return false
	}
	slog.Warn("Rolled back an interrupted dependency migration",
		"dependency", j.ID, "from", j.From, "to", j.To, "wasRunning", j.WasRunning)
	return j.WasRunning
}

// recoverLoadedMigration is the load-path entry point: it runs recovery for a
// namespace that loadNamespace has just produced but nothing has installed or
// started yet. Every load path calls it — the boot path (which also acts on
// the returned restart hint) and installLoadedNamespace (namespace switch,
// activate, auto-activate after create), which deliberately does NOT
// auto-start: a user-initiated switch never starts a namespace, and the
// journal's WasRunning describes a PREVIOUS process, not this session.
func (d *Daemon) recoverLoadedMigration(ctx context.Context, loaded *loadedNamespace, wsID string) bool {
	if loaded == nil || loaded.Runtime == nil {
		return false
	}
	return d.recoverInterruptedMigration(ctx, recoveryActiveNamespace(loaded, wsID))
}

// recoverThenStartLoadedNamespace is the BOOT path's post-load sequence, and
// the order is the whole point of the function: an interrupted migration is
// rolled back BEFORE the runtime is allowed to touch anything, because one of
// the temp containers the journal describes has the namespace's own data
// volume mounted — starting the namespace over it would put a second server on
// one PGDATA. The rollback also decides the start: an interrupted migration
// stopped the namespace on the user's behalf, so handing it back running is
// the rollback's contract (and a rollback that FAILED hands back nothing).
//
// Extracted from Start so that order is testable; keeping the two statements
// inline made deleting either of them a silent, test-free change.
//
// Note what this deliberately does NOT do: import a snapshot. That is a USER
// action only — namespace creation with a selected snapshot, or an explicit
// import — and a `snapshot:` field in the config is a record of where the
// namespace came from, not a trigger. Re-importing it on boot would clobber
// the namespace's live volumes.
func (d *Daemon) recoverThenStartLoadedNamespace(ctx context.Context, loaded *loadedNamespace, wsID string) {
	if loaded == nil || loaded.Runtime == nil {
		return
	}
	if d.recoverLoadedMigration(ctx, loaded, wsID) {
		loaded.ShouldStart = true
	}
	if loaded.ShouldStart {
		// Boot auto-start is not the explicit Update & Start action — no
		// :snapshot pre-pull digest refresh (startRuntime passes false).
		d.startRuntime(loaded.Runtime, loaded.AppDefs)
	}
}

// recoveryActiveNamespace is the activeNamespace snapshot a freshly loaded
// namespace would have. Recovery runs BEFORE the namespace is installed (it
// must precede the runtime), so d.active() would answer about the PREVIOUS
// active namespace — or about nothing at all on the boot path — but the Env
// must still be built through newDepsEnv, which is what wires the Docker
// client, the seeding probe and the stop-wait budget together.
func recoveryActiveNamespace(loaded *loadedNamespace, wsID string) activeNamespace {
	return activeNamespace{
		workspaceID:     wsID,
		nsConfig:        loaded.NsConfig,
		bundleDef:       loaded.BundleDef,
		workspaceConfig: loaded.WorkspaceConfig,
		appDefs:         loaded.AppDefs,
		systemSecrets:   loaded.SystemSecrets,
		volumesBase:     loaded.VolumesBase,
		runtime:         loaded.Runtime,
		dockerClient:    loaded.DockerClient,
	}
}
